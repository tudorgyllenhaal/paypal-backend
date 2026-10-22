FROM public.ecr.aws/docker/library/golang:1.25 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /server ./cmd/server

FROM public.ecr.aws/docker/library/alpine:3.22
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 app
RUN wget -q -O /etc/ssl/certs/aws-rds-bundle.pem https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem
WORKDIR /app
COPY --from=builder /server /app/server
COPY configs/aws.json /app/configs/aws.json
USER app
EXPOSE 8080
ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/configs/aws.json"]
