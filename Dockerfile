FROM golang:1.22 AS builder

ENV \
  GO111MODULE=on \
  CGO_ENABLED=0 \
  GOOS=linux \
  GOARCH=amd64

WORKDIR /go/src/github.com/NEWSOROS/alert2gsm
ADD go.mod go.sum /go/src/github.com/NEWSOROS/alert2gsm/
RUN go mod download

ADD . .

RUN go build -trimpath -ldflags="-s -w" -o /go/bin/alert2gsm .


FROM debian:bookworm-slim

RUN apt-get update -q && apt-get install -yq ca-certificates && rm -rf /var/lib/apt/lists/*
USER nobody
ENV PATH='/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'
CMD ["/go/bin/alert2gsm"]

COPY --from=builder /go/bin /go/bin
