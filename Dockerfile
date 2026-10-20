FROM golang:1.25 AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server .

FROM alpine:3.22
RUN apk add --no-cache wget
COPY --from=builder /app/server /server
EXPOSE 8080
CMD ["/server"]