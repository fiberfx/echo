#!/bin/sh
# mx-echo.rows.sh: the rows of MX Echo (github.com/fiberfx/mx-echo/v6, Go 1.27.4), for flow:row-container.
#
# It starts in /w, where the tree of the revision is unpacked, and its exit status is the result. The container is
# offline: the Go toolchain comes with the image (mx-trafikist-toolchain:go1.27.4-rust1.100.0-r2; ecd-fiberfx-worker
# images carry Go 1.26 and neither testify nor x/net, so they cannot build this module) and the module graph comes from
# the inputs folder, a file:// module proxy at go.sum's pins: /w/inputs/gomod (staged by echo-stand, see STAGED.txt).
#
# Rows, each "ROW PASS|FAIL name (seconds)":
#   modules      go mod download from the proxy
#   gofmt        gofmt -l of reqid and fibervisor is empty
#   vet          go vet ./...
#   test         go test -count=1 ./...
#   race         go test -race -count=1 ./reqid/... ./fibervisor/...
#   purity       reqid's purity test passes, its dependencies hold none of os, log, log/slog, net, fmt, and its
#                non-test files read no clock, environment, process id or random source
#   conformance  the Ecd ID vectors (reqid/testdata/eid) are read both ways, every invalid row is refused with its reason
#   legacy-free  no file of the tree names the removed id scheme (its package, its text form, its radix or its encoder)
#                outside CHANGELOG.md and the vendored registry copy reqid/testdata/eid/systems.tsv (which names retired brd by its old
#                name); the pattern is built from pieces so that this file does not name it
#   cor-systems  a COR is accepted only from a detached-class system or from a registered system with a live cor row (today
#                mxe): ecd and brd are refused, mxe and detached are accepted, and the compiled set equals the one derived
#                from the vendored registry (reqid/testdata/eid/systems.tsv, kinds.tsv), and a minter on an emq lease mints no COR
#   read-correlation  ReadCorrelation gives the COR, its carrier and a reason for each carrier (ok, absent, escape, each strict-parse
#                refusal, cor-kind, cor-system) without allocating, and CorrelationOf is its Correlation
#   mutant-pad   a copy whose pad check is wrong (last&3 becomes last&2) turns TestEIDVectorsInvalid red
#   mutant-count a copy whose minter does not count (count = m.count + 1 becomes count = 0) turns TestEIDVectorsMint red
#   mutant-cor   a copy whose COR system check accepts every system (if system.Class() == Detached becomes if true) turns
#                TestEcdCorRefused red
#   mutant-reason  a copy whose ReadCorrelation names a refused system another reason (return EID{}, ReasonCorSystem becomes
#                return EID{}, ReasonCorKind) turns TestReadCorrelation red
#   mutant-mint-cor  a copy whose Minter.Correlation skips the COR system check (err != nil || !checkCorrelation(id) becomes
#                err != nil) turns TestMinterCorRefused red
# Exit 0 only when every row passed.
set -u
cd /w || exit 2
I=/w/inputs
mkdir -p /tmp/home /tmp/gopath /tmp/gocache
export HOME=/tmp/home GOPATH=/tmp/gopath GOCACHE=/tmp/gocache
export GOTOOLCHAIN=local GOSUMDB=off GOWORK=off GOPRIVATE= GONOSUMDB=
export GOPROXY="file://$I/gomod" GOFLAGS='-modcacherw -mod=readonly' CGO_ENABLED=1
export LANG=C.UTF-8 LC_ALL=C.UTF-8
fails=0
t0=$(date +%s)

command -v go >/dev/null || { echo "RESULT could-not-measure: no go in the image"; exit 4; }
[ -f go.mod ] && [ -d "$I/gomod" ] || { echo "STOP: no go.mod in the tree, or no inputs/gomod"; exit 2; }
echo "env: $(uname -sm); $(go version); $(nproc) CPUs seen; GOPROXY=$GOPROXY; GOPATH=$GOPATH; HOME=$HOME"
echo "module: $(sed -n 's/^module //p' go.mod); go.mod sha256 $(sha256sum go.mod | cut -c1-16), go.sum $(sha256sum go.sum | cut -c1-16)"

# row NAME RC START: one line with the verdict and the seconds since START
row() {
	s=$(($(date +%s) - $3))
	if [ "$2" = 0 ]; then echo "ROW PASS $1 (${s} s)"; else echo "ROW FAIL $1 (rc $2, ${s} s)"; fails=$((fails + 1)); fi
}
# show FILE [N]: the first N lines of a log without the download noise
show() { cut -c1-300 "$1" | grep -v -e '^go: downloading' -e '^ok ' -e '^?  ' | head -"${2:-40}"; }

s=$(date +%s)
go mod download >/tmp/dl.log 2>&1
r=$?
show /tmp/dl.log 10
row "modules: go mod download from the offline proxy" $r "$s"

s=$(date +%s)
gofmt -l reqid fibervisor >/tmp/fmt.log 2>&1
r=$?
[ -s /tmp/fmt.log ] && { cat /tmp/fmt.log; r=1; }
row "gofmt -l reqid fibervisor is empty" $r "$s"

s=$(date +%s)
go vet ./... >/tmp/vet.log 2>&1
r=$?
show /tmp/vet.log
row "vet: go vet ./..." $r "$s"

s=$(date +%s)
go test -count=1 ./... >/tmp/test.log 2>&1
r=$?
grep -E '^(ok|FAIL|---|panic)|_test.go:[0-9]+:' /tmp/test.log | cut -c1-300 | head -80
[ $r = 0 ] || { echo "BEGIN-LOG test"; cat /tmp/test.log; echo "END-LOG test"; }
row "test: go test -count=1 ./..." $r "$s"

s=$(date +%s)
go test -race -count=1 ./reqid/... ./fibervisor/... >/tmp/race.log 2>&1
r=$?
grep -E '^(ok|FAIL|---|panic)|_test.go:[0-9]+:|WARNING: DATA RACE' /tmp/race.log | cut -c1-300 | head -60
[ $r = 0 ] || { echo "BEGIN-LOG race"; cat /tmp/race.log; echo "END-LOG race"; }
row "race: go test -race -count=1 ./reqid/... ./fibervisor/..." $r "$s"

s=$(date +%s)
go test -count=1 -v -run '^TestImportsStayPure' ./reqid/ >/tmp/purity.log 2>&1
r=$?
grep -E '^(--- |ok|FAIL)' /tmp/purity.log
[ "$(grep -c '^--- PASS: TestImportsStayPure' /tmp/purity.log)" -ge 2 ] || r=1
go list -deps ./reqid >/tmp/deps.log 2>&1 || r=1
if grep -x -E 'os|log|log/slog|net|fmt' /tmp/deps.log; then echo "reqid depends on a package it must not"; r=1; fi
if grep -n -E 'time\.Now|os\.Getenv|os\.Getpid|os\.Environ|rand\.Read|"crypto/rand"|"os"|"time"' reqid/*.go | grep -v -E '_test\.go:|^[^:]+:[0-9]+:[[:space:]]*//'; then
	echo "a non-test file of reqid reads a clock, the environment, a pid or a random source"
	r=1
fi
row "purity: reqid's imports and reads" $r "$s"

s=$(date +%s)
go test -count=1 -v -run '^(TestEIDVectorsValid|TestEIDVectorsInvalid|TestEIDInvalidViolation|TestEIDBinaryIngress|TestEIDVectorsLease|TestEIDVectorsMint|TestEIDTextOrderIsByteOrder)$' ./reqid/ >/tmp/conf.log 2>&1
r=$?
grep -E '^(--- |ok|FAIL)' /tmp/conf.log
n=$(grep -c '^--- PASS' /tmp/conf.log)
[ "$n" = 7 ] || { echo "conformance: $n of 7 tests passed"; r=1; }
row "conformance: the Ecd ID vectors, both ways, refusals with reasons" $r "$s"

s=$(date +%s)
go test -count=1 -v -run '^(TestEcdCorRefused|TestBrdCorRefused|TestMxeCorAccepted|TestDetachedCorAccepted|TestCorSystemsMatchRegistry|TestMinterCorRefused)$' ./reqid/ >/tmp/cor.log 2>&1
r=$?
grep -E '^(--- |ok|FAIL)|_test.go:[0-9]+:' /tmp/cor.log | cut -c1-300 | head -30
n=$(grep -c '^--- PASS' /tmp/cor.log)
[ "$n" = 6 ] || { echo "cor-systems: $n of 6 tests passed"; r=1; }
row "cor-systems: a COR only from a detached-class or live-cor registered system" $r "$s"

s=$(date +%s)
go test -count=1 -v -run '^(TestReadCorrelation|TestReadCorrelationAbsent|TestReadCorrelationEscapeAndLength|TestReadCorrelationCarriers|TestReadCorrelationSourceNames|TestReadCorrelationAllocs)$' ./reqid/ >/tmp/read.log 2>&1
r=$?
grep -E '^(--- |ok|FAIL)|_test.go:[0-9]+:' /tmp/read.log | cut -c1-300 | head -30
n=$(grep -c '^--- PASS' /tmp/read.log)
[ "$n" = 6 ] || { echo "read-correlation: $n of 6 tests passed"; r=1; }
row "read-correlation: a COR with its carrier and a reason per carrier, no allocation" $r "$s"

s=$(date +%s)
old='brd''14|brand''ed|Encode''Bran''ded|Base''62'
hits=$(grep -r -n -i -E "$old" --exclude=CHANGELOG.md --exclude=systems.tsv --exclude-dir=inputs --exclude-dir=rows --exclude-dir=.git . | cut -c1-200 | head)
[ -n "$hits" ] && echo "$hits"
[ -z "$hits" ]
row "legacy-free: the removed id scheme is named nowhere outside CHANGELOG.md and the vendored systems.tsv" $? "$s"

# mutant FILE FROM TO NAME TESTRE: a copy of the tree with one line of FILE changed must turn the named test red,
# and the change must be exactly one line.
mutant() {
	s=$(date +%s)
	rm -rf /tmp/mut
	mkdir /tmp/mut
	tar -cf - --exclude=./inputs . | tar -xf - -C /tmp/mut
	sed -i "s/$2/$3/" "/tmp/mut/$1"
	d=$(diff "$1" "/tmp/mut/$1" | grep -c '^>')
	(cd /tmp/mut && go test -count=1 -v -run "^$5\$" ./reqid/) >/tmp/mutant.log 2>&1
	rc=$?
	grep -E '^(--- |ok|FAIL)|_test.go:[0-9]+:' /tmp/mutant.log | cut -c1-260 | head -8
	if [ "$d" = 1 ] && [ $rc != 0 ] && grep -q "^--- FAIL: $5" /tmp/mutant.log; then
		row "$4: the planted fault turns $5 red" 0 "$s"
	else
		echo "mutant: lines changed $d, go test rc $rc"
		row "$4: the planted fault turns $5 red" 1 "$s"
	fi
	rm -rf /tmp/mut
}
mutant reqid/eid.go 'if last\&3 != 0 {' 'if last\&2 != 0 {' mutant-pad TestEIDVectorsInvalid
mutant reqid/eid_mint.go 'count = m.count + 1' 'count = 0' mutant-count TestEIDVectorsMint
mutant reqid/correlation.go 'if system.Class() == Detached {' 'if true {' mutant-cor TestEcdCorRefused
mutant reqid/correlation.go 'return EID{}, ReasonCorSystem' 'return EID{}, ReasonCorKind' mutant-reason TestReadCorrelation
mutant reqid/eid_mint.go 'if err != nil || !checkCorrelation(id) {' 'if err != nil {' mutant-mint-cor TestMinterCorRefused

echo "total $(($(date +%s) - t0)) s"
if [ $fails = 0 ]; then echo "RESULT PASS"; exit 0; fi
echo "RESULT FAIL ($fails rows)"
exit 1
