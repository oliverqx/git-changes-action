#!/bin/zsh
GOOS=linux GOARCH=amd64 go build -o git-changes-action .
docker buildx build -t ghcr.io/oliverqx/git-changes-action:amd64 . --platform=linux/amd64
docker push ghcr.io/oliverqx/git-changes-action:amd64

