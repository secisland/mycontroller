#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

bash $GOPATH/src/github.com/secyu/mycontroller/vendor/k8s.io/code-generator/generate-groups.sh "deepcopy,client,informer,lister" \
  github.com/secyu/mycontroller/pkg/generated \
  github.com/secyu/mycontroller/pkg/apis \
  mycontroller:v1 \
  --output-base "$GOPATH/src" \
  --go-header-file "$GOPATH/src/github.com/secyu/mycontroller/hack/boilerplate.go.txt"
