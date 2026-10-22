# PayPal Server

A Go HTTP server built with Hertz and a MySQL connection pool, supporting local execution and AWS ECS Fargate deployment. The server verifies database access before starting HTTP and closes the pool on shutdown.

## Project structure

```text
server/
├── cmd/server/main.go     # Entry point; loads configuration and starts HTTP server
├── internal/
│   ├── config/config.go   # JSON configuration loading and validation
│   ├── database/mysql.go  # MySQL pool, TLS, timeouts, and startup connection check
│   ├── httpserver/server.go # Hertz server initialization
│   ├── router/router.go   # API route registration
│   └── handler/           # HTTP handlers (ping and health)
├── compose.yaml          # Local MySQL 8.4 container and persistent volume
├── Dockerfile            # Linux amd64 server image for CodeBuild/ECS
├── buildspec.yml         # CodeBuild image build and ECR push
├── configs/
│   ├── local.json         # Local HTTP and MySQL settings
│   └── aws.json           # AWS HTTP/MySQL defaults; ECS supplies database credentials
├── deploy/aws/
│   ├── stack.yaml         # ECS Fargate CloudFormation template
│   ├── parameters.example.json # Deployment parameter template
│   ├── config.example     # AWS CLI region/profile template
│   └── credentials.example # AWS credential template
├── .gitignore             # Excludes private configuration and build output
├── go.mod                 # Go module and dependencies
├── go.sum                 # Dependency checksums
└── README.md              # Project structure and run instructions
```

The server exposes `GET /ping`, which returns `pong`, and `GET /healthz`, which returns `{"status":"ok"}`. Add handlers in `internal/handler` and register routes in `internal/router/router.go`. Server initialization lives in `internal/httpserver`.

## Run locally

Prerequisites: Go 1.22.2 or later, Docker with Compose, and a running Docker daemon.

From this directory (`source/server`):

```sh
docker compose up -d --wait --wait-timeout 180 mysql
go mod download
go run ./cmd/server -config configs/local.json
```

In another terminal:

```sh
curl --fail http://127.0.0.1:8080/ping
curl --fail http://127.0.0.1:8080/healthz
```

Stop the server with `Ctrl+C`. The server handles SIGINT and SIGTERM with graceful shutdown and closes its database pool.

MySQL runs in Docker on `127.0.0.1:3306` with database `paypal`, application user `paypal`, and password `paypal_local`. These development credentials match `configs/local.json`. The root password is `paypal_root_local`. MySQL data persists in the Compose `mysql-data` volume; initialization credentials apply only when the volume is first created. If port 3306 is occupied, change the published host port in `compose.yaml` and `mysql.port` in `configs/local.json` together.

Stop MySQL while retaining its data:

```sh
docker compose stop mysql
```

Check MySQL status or open its SQL console (enter `paypal_local` when prompted):

```sh
docker compose ps mysql
docker compose exec mysql mysql -u paypal -p paypal
```

Run unit checks and the database integration test with MySQL running:

```sh
go test ./...
MYSQL_INTEGRATION_TEST=1 go test ./internal/database -v
```

### Server configuration

Settings are centralized in JSON files selected with `-config`:

| Setting | Local default | AWS default |
| --- | --- | --- |
| `mode` | `local` | `aws` |
| `http.address` | `127.0.0.1:8080` | `0.0.0.0:8080` |
| `http.read_timeout_seconds` | `10` | `10` |
| `http.write_timeout_seconds` | `10` | `10` |
| `http.shutdown_timeout_seconds` | `20` | `20` |

For private local settings, copy `configs/local.json` to `configs/local.private.json`, edit it, and pass that path to `-config`. Files matching `configs/*.private.json` are ignored by Git.

### MySQL configuration

The `mysql` section in each JSON configuration defines the connection and pool settings:

| JSON key | Local default | AWS default / override |
| --- | --- | --- |
| `host` | `127.0.0.1` | `MYSQL_HOST` from deployment |
| `port` | `3306` | `MYSQL_PORT` from deployment |
| `database` | `paypal` | `MYSQL_DATABASE` from deployment |
| `username` | `paypal` | `MYSQL_USER` from deployment |
| `password` | `paypal_local` | `MYSQL_PASSWORD` injected from Secrets Manager |
| `tls` | `false` | `true`, overridden by `MYSQL_TLS` |
| `tls_ca_file` | Empty | `/etc/ssl/certs/aws-rds-bundle.pem`, overridden by `MYSQL_TLS_CA_FILE` |
| `connect_timeout_seconds` | `5` | `5` |
| `io_timeout_seconds` | `10` | `10` |
| `startup_timeout_seconds` | `15` | `15` |
| `max_open_conns` | `10` | `10` |
| `max_idle_conns` | `5` | `5` |
| `conn_max_lifetime_seconds` | `300` | `300` |

Environment variables listed above override JSON values, including when set to an empty string. Host, database, username, and password are required. Startup fails if configuration is invalid or MySQL cannot be reached or authenticated. The connection pool uses `database/sql` and `go-sql-driver/mysql`, with date/time parsing enabled.

AWS TLS verifies the database hostname against system certificates plus the AWS RDS CA bundle downloaded when building the image. For another certificate authority, provide a PEM file in the image or mounted volume and set `MYSQL_TLS_CA_FILE` to its path. To use system CAs alone, set that variable to an empty string. Local MySQL uses an unencrypted connection on the loopback interface.

## Deploy and run on AWS

The supplied CloudFormation template creates an ECS cluster, one Fargate task managed by an ECS service, IAM execution/task roles, a security group, a CloudWatch log group, and a Secrets Manager secret for the database password. The task runs Linux amd64 with 256 CPU units and 512 MiB memory. HTTP is exposed directly on the task's public IP at port 8080.

Prerequisites:

- AWS CLI.
- A CodeBuild project configured to build this repository, and an existing ECR repository for the server image.
- An AWS account with permissions to manage ECR and the CloudFormation resources listed above, including Secrets Manager.
- An existing MySQL database and application user with access to it. This deployment connects to your database; it does not create a database instance, schema, or application tables.
- An existing VPC and public subnets in that VPC with routes through an internet gateway.

All following commands run from `source/server`.

### 1. Configure AWS access separately

```sh
cp deploy/aws/credentials.example deploy/aws/credentials
cp deploy/aws/config.example deploy/aws/config
cp deploy/aws/parameters.example.json deploy/aws/parameters.json
```

Edit `deploy/aws/credentials` with your AWS access key and secret key under `[paypal]`. For temporary credentials, also provide `aws_session_token`. Edit `deploy/aws/config` with your AWS region under `[profile paypal]`.

```sh
export AWS_SHARED_CREDENTIALS_FILE="$PWD/deploy/aws/credentials"
export AWS_CONFIG_FILE="$PWD/deploy/aws/config"
export AWS_PROFILE=paypal
aws sts get-caller-identity
```

These actual configuration files are ignored by Git. AWS credentials are used by deployment commands. The server currently makes no AWS API calls; the ECS task role is available for future AWS integrations.

### 2. Build the server image

Use the existing CodeBuild project and `buildspec.yml` at the repository root (`server`). CodeBuild builds `Dockerfile` for Linux amd64 and pushes it to ECR. The Dockerfile includes `configs/aws.json`, CA certificates, and the RDS CA bundle. Database credentials are supplied at runtime. Configure the CodeBuild environment for Docker access and ECR push permissions, with the ECR repository already created. Use a new image tag for updated code.

### 3. Set deployment parameters

Edit `deploy/aws/parameters.json`:

| Parameter | Value |
| --- | --- |
| `ImageUri` | Full existing image URI, such as `123456789012.dkr.ecr.us-east-1.amazonaws.com/paypal-server:v1` |
| `VpcId` | Existing VPC ID |
| `SubnetIds` | Comma-separated public subnet IDs belonging to that VPC |
| `AllowedCidr` | Client CIDR allowed to access port 8080; `0.0.0.0/0` permits all IPv4 clients |
| `DatabaseHost` | Your existing MySQL hostname, without a port or URL scheme |
| `DatabasePort` | MySQL port, default `3306` |
| `DatabaseName` | Existing database name, default `paypal` |
| `DatabaseUser` | Existing MySQL application user |
| `DatabasePassword` | That user's password; CloudFormation masks the parameter and stores it in Secrets Manager |
| `DatabaseTLS` | `true` by default; use `false` only if your database requires an unencrypted connection |
| `DatabaseSecurityGroupId` | Optional existing database security group ID; when set, the stack allows the ECS security group to connect on `DatabasePort` |

Keep real credentials in the ignored `deploy/aws/parameters.json`, not the example file. ECS injects host, port, database, user, and TLS settings as environment variables and the password through Secrets Manager. The template grants the task execution role permission to read that secret; no AWS access keys are passed to the server.

The database must be reachable from the ECS subnets. For a database in the same VPC, set `DatabaseSecurityGroupId` to its security group ID to create the ingress rule automatically. Otherwise, ensure the database firewall and network routes permit ECS access. The stack outputs `ServerSecurityGroupId` for this purpose.

Keep the AWS server listen port at 8080 unless you also update the container port, security group, and health check in `stack.yaml`.

### 4. Create the deployment

```sh
aws cloudformation create-stack \
  --stack-name paypal-server \
  --template-body file://deploy/aws/stack.yaml \
  --parameters file://deploy/aws/parameters.json \
  --capabilities CAPABILITY_IAM
aws cloudformation wait stack-create-complete --stack-name paypal-server
aws cloudformation describe-stacks \
  --stack-name paypal-server --query 'Stacks[0].Outputs'
```

### 5. Access the running server

Use the stack outputs `ClusterName` and `ServiceName` to locate the service in the ECS console. Open its running task, follow the network interface link, and copy the public IPv4 address.

```sh
curl --fail http://PUBLIC_IP:8080/healthz
```

The public IP changes when the task is replaced. Container logs are available in the CloudWatch log group named by the stack's `LogGroup` output. ECS checks `/healthz` every 30 seconds.

### 6. Update the deployment

Supply an updated image URI, then update `ImageUri` in `deploy/aws/parameters.json`:

```sh
aws cloudformation update-stack \
  --stack-name paypal-server \
  --template-body file://deploy/aws/stack.yaml \
  --parameters file://deploy/aws/parameters.json \
  --capabilities CAPABILITY_IAM
aws cloudformation wait stack-update-complete --stack-name paypal-server
```

If only the database password changes, running tasks retain their injected value. After updating the stack, restart the service to load the updated secret, substituting the cluster and service names from stack outputs:

```sh
aws ecs update-service --cluster CLUSTER_NAME --service SERVICE_NAME --force-new-deployment
```

References: [MySQL Go driver](https://github.com/go-sql-driver/mysql), [MySQL Docker image](https://hub.docker.com/_/mysql), [ECS secret injection](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/secrets-envvar-secrets-manager.html).
