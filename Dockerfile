FROM golang:alpine AS builder

RUN apk add --quiet --no-cache build-base git

WORKDIR /src

ENV GO111MODULE=on

ADD go.* ./

RUN go mod download

ADD . .

RUN go install -tags osusergo,netgo -ldflags="-s -w -extldflags=-static"


FROM alpine

RUN apk add --quiet --no-cache git

COPY --from=builder /go/bin/* /usr/bin/

CMD ["app2kube"]
