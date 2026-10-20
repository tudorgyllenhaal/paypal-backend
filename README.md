# PayPal Server

A Go HTTP server built with Hertz, supporting local execution and AWS ECS Fargate deployment.

## Project structure

```text
server/
├── cmd/server/main.go     # Entry point; loads configuration and starts HTTP server
├── internal/
│   ├── config/config.go   # JSON configuration loading and validation
│   ├── httpserver/server.go # Hertz server initialization
│   ├── router/router.go   # API route registration
│   └── handler/           # HTTP handlers (ping and health)
├── configs/
│   ├── local.json         # Local server settings
│   └── aws.json           # Container/AWS server settings
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

Prerequisite: Go 1.22.2 or later.

From this directory (`source/server`):

```sh
go mod download
go run ./cmd/server -config configs/local.json
```

In another terminal:

```sh
curl --fail http://127.0.0.1:8080/ping
curl --fail http://127.0.0.1:8080/healthz
```

Stop the server with `Ctrl+C`. The server handles SIGINT and SIGTERM with graceful shutdown.

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

## Deploy and run on AWS

The supplied CloudFormation template creates an ECS cluster, one Fargate task managed by an ECS service, IAM execution/task roles, a security group, and a CloudWatch log group. The task runs Linux amd64 with 256 CPU units and 512 MiB memory. HTTP is exposed directly on the task's public IP at port 8080.

Prerequisites:

- AWS CLI.
- An existing Linux amd64 server image in ECR that listens on port 8080 and supports `/healthz`. The ECS health check also requires `wget` in the image. Image packaging is supplied separately.
- An AWS account with permissions to manage ECR and the CloudFormation resources listed above.
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

### 2. Set deployment parameters

Edit `deploy/aws/parameters.json`:

| Parameter | Value |
| --- | --- |
| `ImageUri` | Full existing image URI, such as `123456789012.dkr.ecr.us-east-1.amazonaws.com/paypal-server:v1` |
| `VpcId` | Existing VPC ID |
| `SubnetIds` | Comma-separated public subnet IDs belonging to that VPC |
| `AllowedCidr` | Client CIDR allowed to access port 8080; `0.0.0.0/0` permits all IPv4 clients |

Keep the AWS server listen port at 8080 unless you also update the container port, security group, and health check in `stack.yaml`.

### 3. Create the deployment

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

### 4. Access the running server

Use the stack outputs `ClusterName` and `ServiceName` to locate the service in the ECS console. Open its running task, follow the network interface link, and copy the public IPv4 address.

```sh
curl --fail http://PUBLIC_IP:8080/healthz
```

The public IP changes when the task is replaced. Container logs are available in the CloudWatch log group named by the stack's `LogGroup` output. ECS checks `/healthz` every 30 seconds.

### 5. Update the deployment

Supply an updated image URI, then update `ImageUri` in `deploy/aws/parameters.json`:

```sh
aws cloudformation update-stack \
  --stack-name paypal-server \
  --template-body file://deploy/aws/stack.yaml \
  --parameters file://deploy/aws/parameters.json \
  --capabilities CAPABILITY_IAM
aws cloudformation wait stack-update-complete --stack-name paypal-server
```
