# PayPal Server

A Go HTTP server built with Hertz and a MySQL connection pool, supporting local execution and AWS ECS Fargate deployment. The server verifies database access before starting HTTP and closes the pool on shutdown.

## Project structure

```text
server/
├── cmd/server/main.go     # HTTP server entry point
├── cmd/paypal-check/      # PayPal check configuration, report logic and unit tests
├── cmd/paypal-payment/    # Sandbox order creation and capture tool
├── internal/
│   ├── config/config.go   # JSON configuration loading and validation
│   ├── paypal/            # Shared OAuth, Transaction Search, webhook and sandbox order APIs
│   ├── database/mysql.go  # MySQL pool, TLS, timeouts, and startup connection check
│   ├── httpserver/server.go # Hertz server initialization
│   ├── router/router.go   # API route registration
│   └── handler/           # HTTP handlers (ping and health)
├── compose.yaml          # Local MySQL 8.4 container and persistent volume
├── Dockerfile            # Linux amd64 server image for CodeBuild/ECS
├── buildspec.yml         # CodeBuild image build and ECR push
├── configs/
│   ├── paypal.example.json # PayPal checker configuration template
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

## Check PayPal integration dependencies

Run this separate command before implementing imports. `cmd/paypal-check` owns configuration, check decisions, and report generation; `internal/paypal` owns authentication and API requests and is shared with `cmd/paypal-payment`. It does not start the HTTP server, connect to MySQL, register webhooks, or write payments. Its only POST request obtains an OAuth token; history and subscription checks use GET requests.

```sh
cp configs/paypal.example.json configs/paypal.private.json
```

Edit `configs/paypal.private.json`:

- Set `environment` to `sandbox` (or explicitly `live` for a live app).
- Provide the application's `client_id` and `client_secret`. Alternatively, supply `PAYPAL_CLIENT_ID` and `PAYPAL_CLIENT_SECRET` through the environment; these override file values.
- Use `lookback_hours` (default `24`, maximum `744`) for a rolling query ending at the current UTC time. The range is computed once per run and reused for every page. A positive `lookback_hours` takes precedence over dates. For a fixed historical query, omit it or set it to `0` and provide both `start_at` and `end_at` as RFC3339 timestamps, at most 31 days apart. If both dates are omitted, the checker defaults to the last 24 hours.
- Set `max_pages` to limit requests (1–1000). Each page requests up to 500 entries, including balance-affecting and non-balance-affecting records. Reaching the page limit leaves coverage unverified.
- Set `webhook_url` to the exact existing HTTPS listener URL and `expected_webhook_events` to the event names your accessible PayPal product requires, such as `PAYMENT.CAPTURE.COMPLETED`. The checker also recognizes an existing wildcard subscription. It does not select or change subscriptions for you.
- Set `expected_transaction_event_codes` to the reporting event codes expected from your known sandbox scenarios. Empty expectations, empty results, missing codes, or partial retrieval remain unverified.

```sh
go run ./cmd/paypal-check -config configs/paypal.private.json
```

The command prints a JSON report with `confirmed`, `failed`, and `unverified` checks, transaction counts, and observed event codes. It excludes credentials, tokens, resource IDs, URLs, amounts, and raw payment payloads. Errors do not include PayPal response bodies. A failed check, invalid configuration, or output error produces a nonzero exit status. Exit status zero means the command ran without failed checks; it does **not** mean all dependencies are confirmed. Inspect every check's status.

A `401` during a GET refreshes the token and retries once. Missing permissions, rate limits, and other failures produce actionable reports; this diagnostic command does not perform background retries. Requests have a 20-second timeout, and redirects are not followed.

Identity mappings, consumer-history access, source-record identity checks, and the `-samples` option are temporarily commented out. The checker currently reports only authorization, history access, sandbox coverage, and webhook subscriptions.

<!-- Temporarily disabled identity-mapping documentation.
### Optional identity samples

To compare captured API resources with retrieved reporting entries, save same-environment samples in `configs/paypal-samples.private.json`. The following shows the input structure; replace placeholders with actual matching sample identifiers. Additional fields from API resource payloads are accepted.

```json
{
  "environment": "sandbox",
  "captures": [{"id": "CAPTURE_ID"}],
  "refunds": [{
    "id": "REFUND_ID",
    "links": [{
      "rel": "up",
      "href": "https://api-m.sandbox.paypal.com/v2/payments/captures/CAPTURE_ID"
    }]
  }],
  "webhooks": [{
    "event_type": "PAYMENT.CAPTURE.COMPLETED",
    "resource_type": "capture",
    "resource": {"id": "CAPTURE_ID"}
  }]
}
```

```sh
go run ./cmd/paypal-check -config configs/paypal.private.json \
  -samples configs/paypal-samples.private.json
```

A sample capture must have exactly one reporting entry with the same transaction ID. A refund must additionally have an explicit capture relationship matching reporting's `paypal_reference_id` with reference type `TXN`. The capture relationship may come from `supplementary_data.related_ids.capture_id` or a PayPal `up` link to a capture resource; conflicting relationships remain unresolved. Supported webhook samples identify a capture/refund resource and use the corresponding payment event type. Missing, unsupported, or ambiguous identities remain unverified; there is no matching by amount, date, or merchant.

These checks establish relationships only for the supplied samples, whose authenticity and webhook signatures are not verified by this command. They do not prove a general stable source-record key. An application token and successful Transaction Search request also do not establish third-party consumer consent or partner authorization. The report deliberately keeps those dependencies unverified. Webhook subscription presence does not prove delivery, signature verification, or complete consumer purchase coverage.

Both private JSON files are covered by the existing `configs/*.private.json` Git ignore rule. No real credentials or private samples belong in the example configuration or test fixtures.

-->

### Unit tests and overall coverage

The PayPal tests use local mock HTTP servers and require no PayPal credentials or MySQL instance. From the server repository root:

```sh
mkdir -p bin
go test ./... -coverprofile=bin/coverage.out
go tool cover -func=bin/coverage.out
go vet ./...
```

Use the `total: (statements)` line to assess overall coverage across all Go packages, including the server entry point. The implementation must keep this total above 80%; coverage output is ignored under `bin/`.

PayPal references: [Transaction Search behavior and identifiers](https://developer.paypal.com/api/transaction-search/v1/search-get), [partner access requirement](https://github.com/paypal/paypal-rest-api-specifications/blob/main/openapi/reporting_transactions_v1.json), [existing webhook registrations](https://developer.paypal.com/api/webhooks/v1/webhooks-list/).

## Create sandbox payments for history testing

`cmd/paypal-payment` creates and captures PayPal sandbox checkout orders so you can query actual test transactions with `paypal-check`. It runs separately from the HTTP server and MySQL. The API host is fixed to `api-m.sandbox.paypal.com`; live configuration is rejected before any network request.

Use the **sandbox seller application's** credentials in `configs/paypal.private.json`, the same file used by the history checker. The payment tool reads only `environment`, `client_id`, and `client_secret`; history dates and other checker settings are ignored. `PAYPAL_CLIENT_ID` and `PAYPAL_CLIENT_SECRET` override file credentials. A separate sandbox personal/buyer account approves the purchase in a browser. Do not use the seller account as the buyer.

### 1. Create an order

From the server repository root:

```sh
go run ./cmd/paypal-payment create \
  -config configs/paypal.private.json \
  -amount 1.25 -currency USD \
  -request-id sandbox-create-001
```

The JSON result includes `order_id`, `status`, and `approval_url`. Creating the order alone does not complete a payment.

Amounts are exact decimal strings, with at most nine integer digits and two decimal places; no floating-point conversion is used. Currency defaults to `USD`; PayPal determines supported currencies for the seller. `JPY`, `HUF`, and `TWD` require integer amounts.

### 2. Approve as the sandbox buyer

Open the returned `approval_url`, sign in with a separate sandbox buyer account, and approve the purchase. Use a separate browser profile if you are already signed in as the seller.

The return and cancel URLs both default to `http://localhost:8080/ping`. Run the local HTTP server if you want that page to respond, or supply your own callback URLs when creating the order:

```sh
go run ./cmd/paypal-payment create \
  -config configs/paypal.private.json \
  -amount 1.25 -currency USD \
  -return-url https://your-app.example/approved \
  -cancel-url https://your-app.example/cancelled \
  -request-id sandbox-create-002
```

Callbacks must use HTTPS, except local loopback HTTP URLs. They do not automatically capture the order. If the local return page is unavailable after approval, continue with capture using the saved order ID; if capture reports that approval is missing, complete approval first.

### 3. Capture the approved order

Replace `ORDER_ID` with the ID returned by create:

```sh
go run ./cmd/paypal-payment capture \
  -config configs/paypal.private.json \
  -order-id ORDER_ID \
  -request-id sandbox-capture-001
```

The JSON result includes the order ID/status and each capture's ID/status. Check for a `COMPLETED` capture before treating it as a completed payment; pending captures do not represent completed payments. The output omits credentials, access tokens, and full buyer/payment payloads.

Every create or capture operation requires a `-request-id` (1–38 letters, digits, dots, underscores, colons, or hyphens), sent as `PayPal-Request-Id`. Use a new key for every distinct operation, including separate keys for create and capture. For a timeout, output failure, or other uncertain outcome, retain the key and retry only the identical operation with the same arguments and key, within PayPal's idempotency retention period. The tool performs no automatic mutation retries and does not follow redirects. Nonzero exit status reports invalid input, API errors, or output failure; it does not guarantee that PayPal did not apply an operation before a network/output failure.

### 4. Retrieve the payment history

Use the history checker's `lookback_hours: 24` to automatically include recent payments, then run:

```sh
go run ./cmd/paypal-check -config configs/paypal.private.json
```

Use the same seller app/account to inspect the incoming payment. A successful sandbox checkout does not establish third-party consumer-history access. Reporting can take up to three hours to expose an executed transaction, and Transaction Search still requires the appropriate permissions.

The payment command's unit tests use mock HTTP servers, covering create/capture requests, sandbox enforcement, exact amount validation, approval handling, idempotency headers, API/network failures, malformed responses, redirects, and credential redaction. They run with the existing full-suite coverage command above; no real PayPal payments are made by the tests.

References: [PayPal create/approve/capture flow](https://developer.paypal.com/api/rest/integration/orders-api), [currency precision](https://developer.paypal.com/api/codes/currency), [Transaction Search reporting delay](https://developer.paypal.com/api/transaction-search/v1/search-get).
