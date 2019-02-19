FROM golang:1.11.1-alpine as builder
WORKDIR /go/src
ARG VERSION=dev
COPY . /go/src/github.com/boz/kail
RUN go install github.com/boz/kail/cmd/kail

FROM alpine
RUN apk update && apk add --no-cache ca-certificates && \
    apk add bash
COPY --from=builder /go/bin/kail /usr/bin/kail
COPY ./kail-wrapper /usr/bin/kail-wrapper
ENTRYPOINT ["kail"]
