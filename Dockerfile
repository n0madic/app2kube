FROM golang:alpine AS builder

RUN apk add --quiet --no-cache build-base git

WORKDIR /go/src/github.com/n0madic/app2kube

ADD . .

ENV GO111MODULE=on

RUN cd cmd/app2kube/ && \
    go build -ldflags="-s -w"


FROM alpine

COPY --from=builder /go/src/github.com/n0madic/app2kube/cmd/app2kube/app2kube /usr/bin/

CMD ["app2kube"]
