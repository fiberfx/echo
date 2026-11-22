#!/bin/sh
# Rows of the mxdb Go client (package mxdb, scaffold/mxdb-client): vet, the tests under the race detector against
# an in-process fake server, the std-only check, and one mutant that maps OutcomeUnknown to Aborted and must turn
# named tests red. The container is offline; the module cache is not needed because the package imports only std.
set -u
cd /w || exit 2
export HOME=/tmp/mxdb-home GOPATH=/tmp/mxdb-gopath GOFLAGS=-mod=readonly GOPROXY=off GOTOOLCHAIN=local
export GOCACHE=/tmp/mxdb-home/.cache/go-build CGO_ENABLED=1
mkdir -p "$HOME" "$GOPATH" "$GOCACHE"
fail=0
row() { # name rc
	if [ "$2" -eq 0 ]; then echo "ROW $1 PASS"; else echo "ROW $1 FAIL rc=$2"; fail=1; fi
}
echo "=== environment"
go version 2>&1 | sed 's/^/    | /'
echo "    | gcc: $(command -v gcc cc 2>/dev/null | tr '\n' ' ')"
echo "    | module cache: $(go env GOMODCACHE) $( [ -d "$(go env GOMODCACHE)" ] && echo present || echo missing )"
echo "    | files: $(ls mxdb | tr '\n' ' ')"
out=$(gofmt -l mxdb 2>&1); [ -z "$out" ]; rc=$?; [ $rc -eq 0 ] || echo "$out" | sed 's/^/    | /'
row "gofmt -l mxdb is empty" $rc
go vet ./mxdb/... > /tmp/vet.log 2>&1; rc=$?; [ $rc -eq 0 ] || head -60 /tmp/vet.log | sed 's/^/    | /'
row "go vet ./mxdb/..." $rc
go test -race -count=1 -v ./mxdb/... > /tmp/test.log 2>&1; rc=$?
echo "    | tests: $(grep -c '^--- PASS' /tmp/test.log) pass, $(grep -c '^--- FAIL' /tmp/test.log) fail"
grep -E '^(--- |ok|FAIL|panic)' /tmp/test.log | sed 's/^/    | /'
[ $rc -eq 0 ] || grep -E -B2 -A12 -E '^ *--- FAIL|^panic|\[build failed\]|^# ' /tmp/test.log | head -120 | sed 's/^/    | /'
row "go test -race ./mxdb/..." $rc
extra=$(go list -deps -test -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./mxdb 2>&1 | grep -v -E '^(github.com/fiberfx/mx-echo/v6/mxdb(\.test| \[.*\])?)?$')
[ -z "$extra" ]; rc=$?; [ $rc -eq 0 ] || echo "$extra" | sed 's/^/    | /'
row "mxdb and its tests import only std" $rc

echo "=== mutant: OutcomeUnknown mapped to Aborted, on a copy"
rm -rf /tmp/mut && mkdir -p /tmp/mut && cp go.mod go.sum /tmp/mut/ && cp -R mxdb /tmp/mut/mxdb
sed 's/return &UnknownError{Cause: cause}/return \&AbortedError{FailedPre: -1}/' mxdb/errors.go > /tmp/mut/mxdb/errors.go
cmp -s mxdb/errors.go /tmp/mut/mxdb/errors.go; changed=$?
[ $changed -ne 0 ]; row "the mutant differs from the source" $?
(cd /tmp/mut && go test -count=1 -v ./mxdb/ > /tmp/mutant.log 2>&1); rc=$?
grep -E '^(--- FAIL|FAIL|ok)' /tmp/mutant.log | sed 's/^/    | /'
[ $rc -ne 0 ] && ! grep -q -E '\[build failed\]|^# ' /tmp/mutant.log; row "the mutant fails its tests and compiles" $?
for t in TestUpdateDroppedConnIsUnknown TestOutcomeClassesDistinct; do
	grep -q "^--- FAIL: $t " /tmp/mutant.log; row "the mutant turns $t red" $?
done
grep -q -E '^--- PASS: TestUpdateOkAndGet ' /tmp/mutant.log; row "the mutant leaves an unrelated test green" $?

if [ $fail -eq 0 ]; then echo "RESULT mxdbclient PASS"; exit 0; fi
echo "RESULT mxdbclient FAIL"; exit 1
