#syntax=docker/dockerfile:1.2
FROM golang:1.17 as builder
WORKDIR /usr/src/app
ADD . .
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
    --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o hcloud-fip-k8s .

FROM alpine:3.13
RUN apk add --no-cache ca-certificates
COPY --from=builder /usr/src/app/hcloud-fip-k8s /bin/
CMD /bin/hcloud-fip-k8s
