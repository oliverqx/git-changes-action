FROM alpine:latest as builder

RUN apk add --no-cache ca-certificates
RUN update-ca-certificates

# add a user here because addgroup and adduser are not available in scratch
RUN addgroup -S gitchanges \
    && adduser -S -u 10000 -g gitchanges gitchanges


FROM scratch
# copy ca certs
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# copy users from builder
COPY --from=builder /etc/passwd /etc/passwd

WORKDIR /git-changes-action
COPY git-changes-action /app/git-changes-action

ENTRYPOINT ["/app/git-changes-action"]
