FROM golang:1.18-alpine AS builder

WORKDIR /polygon-edge

ADD go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o polygon-edge main.go

FROM node:16-buster AS contracts

WORKDIR /contracts

RUN git clone https://github.com/0xPolygon/core-contracts.git -b dev .
RUN npm install && npm run compile

FROM alpine:latest AS runner

RUN apk --no-cache add ca-certificates jq

WORKDIR /polygon-edge

COPY --from=builder /polygon-edge/polygon-edge ./
COPY --from=contracts /contracts/artifacts ./core-contracts/artifacts
COPY ./docker/local/polygon-edge.sh ./

# Expose json-rpc, libp2p and grpc ports
EXPOSE 8545 9632 1478

ENTRYPOINT ["./polygon-edge.sh"]
