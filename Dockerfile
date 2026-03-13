FROM golang:1.26.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /sql-tapd ./cmd/sql-tapd

FROM alpine:3
COPY --from=build /sql-tapd /sql-tapd
ENTRYPOINT ["/sql-tapd"]
