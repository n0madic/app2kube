FROM golang:alpine AS builder

RUN apk add --quiet --no-cache build-base git

WORKDIR /src

ENV GO111MODULE=on

COPY go.* ./

RUN go mod download

COPY . .

RUN go install -tags osusergo,netgo -ldflags="-s -w -extldflags=-static"


FROM alpine

RUN apk add --quiet --no-cache git

COPY --from=builder /go/bin/* /usr/bin/

CMD ["app2kube"]
