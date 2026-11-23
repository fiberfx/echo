#!/bin/sh
# ownerstore.rows.sh: the rows of package ownerstore (github.com/fiberfx/mx-echo/v6/ownerstore, the SQLite owner store),
# for flow:row-container.
#
# It starts in /w, where the tree of the revision is unpacked, and its exit status is the result. The container is
# offline: the Go toolchain comes with the image (mx-trafikist-toolchain:go1.27.4-rust1.100.0-r2) and the module graph
# comes from the inputs folder, a file:// module proxy at go.sum's pins: /w/inputs/gomod (mx-echo-sqlite-in).
#
# Rows, each "ROW PASS|FAIL name (seconds)":
#   modules       go mod download from the proxy
#   gofmt         gofmt -l ownerstore is empty
#   vet           go vet ./ownerstore/ with CGO_ENABLED=0, for linux, windows and darwin
#   test          go test ./ownerstore/ with CGO_ENABLED=0: every test passes
#   race          go test -race ./ownerstore/ with cgo on
#   build         CGO_ENABLED=0 go build ./ownerstore/ for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
#   no-cgo        runtime/cgo is not among the package's dependencies, with cgo on
#   vectors       TestOutcomeVectors checks all 34 steps of outcomes.tsv; TestRowsMapping holds
#   rows.tsv      each required check of rows.tsv passes through the test it is mapped to (16 rows)
#   mutant-*      a copy of the tree with one line changed turns the named test red (11 mutants)
# Exit 0 only when every row passed.
set -u
cd /w || exit 2
I=/w/inputs
mkdir -p /tmp/home /tmp/gopath /tmp/gocache
export HOME=/tmp/home GOPATH=/tmp/gopath GOCACHE=/tmp/gocache
export GOTOOLCHAIN=local GOSUMDB=off GOWORK=off GOPRIVATE= GONOSUMDB=
export GOPROXY="file://$I/gomod" GOFLAGS='-modcacherw -mod=readonly'
export LANG=C.UTF-8 LC_ALL=C.UTF-8
fails=0
t0=$(date +%s)

command -v go >/dev/null || { echo "RESULT could-not-measure: no go in the image"; exit 4; }
[ -f go.mod ] && [ -d "$I/gomod" ] || { echo "STOP: no go.mod in the tree, or no inputs/gomod"; exit 2; }
echo "env: $(uname -sm); $(go version); $(nproc) CPUs seen; GOPROXY=$GOPROXY; gcc: $(command -v gcc cc 2>/dev/null | tr '\n' ' ')"
echo "module: $(sed -n 's/^module //p' go.mod); go.mod sha256 $(sha256sum go.mod | cut -c1-16), go.sum $(sha256sum go.sum | cut -c1-16)"
echo "files: $(ls ownerstore | tr '\n' ' ')"

# row NAME RC START: one line with the verdict and the seconds since START
row() {
	s=$(($(date +%s) - $3))
	if [ "$2" = 0 ]; then echo "ROW PASS $1 (${s} s)"; else echo "ROW FAIL $1 (rc $2, ${s} s)"; fails=$((fails + 1)); fi
}
show() { cut -c1-300 "$1" | grep -v -e '^go: downloading' -e '^ok ' -e '^?  ' | head -"${2:-40}"; }

s=$(date +%s)
CGO_ENABLED=0 go mod download >/tmp/dl.log 2>&1
r=$?
show /tmp/dl.log 10
row "modules: go mod download from the offline proxy" $r "$s"

s=$(date +%s)
gofmt -l ownerstore >/tmp/fmt.log 2>&1
r=$?
[ -s /tmp/fmt.log ] && { cat /tmp/fmt.log; r=1; }
row "gofmt -l ownerstore is empty" $r "$s"

for target in linux/amd64 windows/amd64 darwin/arm64; do
	s=$(date +%s)
	CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} go vet ./ownerstore/ >/tmp/vet.log 2>&1
	r=$?
	show /tmp/vet.log
	row "vet: CGO_ENABLED=0 go vet ./ownerstore/ for $target" $r "$s"
done

s=$(date +%s)
CGO_ENABLED=0 go test -count=1 -timeout 12m -v ./ownerstore/ >/tmp/test.log 2>&1
r=$?
grep -E '^(--- |ok|FAIL|panic)|_test.go:[0-9]+:' /tmp/test.log | cut -c1-300 | grep -v '^--- PASS' | head -80
echo "tests: $(grep -c '^--- PASS' /tmp/test.log) pass, $(grep -c '^--- FAIL' /tmp/test.log) fail, $(grep -c '^--- SKIP' /tmp/test.log) skip (top level)"
[ $r = 0 ] || { echo "BEGIN-LOG test"; head -c 60000 /tmp/test.log; echo "END-LOG test"; }
row "test: CGO_ENABLED=0 go test ./ownerstore/" $r "$s"

s=$(date +%s)
CGO_ENABLED=1 go test -race -count=1 -timeout 20m -v ./ownerstore/ >/tmp/race.log 2>&1
r=$?
grep -E '^(--- FAIL|ok|FAIL|panic)|_test.go:[0-9]+:|WARNING: DATA RACE' /tmp/race.log | cut -c1-300 | head -60
[ $r = 0 ] || { echo "BEGIN-LOG race"; head -c 60000 /tmp/race.log; echo "END-LOG race"; }
row "race: go test -race ./ownerstore/ with cgo on" $r "$s"

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
	s=$(date +%s)
	CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} go build ./ownerstore/ >/tmp/build.log 2>&1
	r=$?
	show /tmp/build.log
	row "build: CGO_ENABLED=0 go build ./ownerstore/ for $target" $r "$s"
done

s=$(date +%s)
CGO_ENABLED=1 go list -deps ./ownerstore/ >/tmp/deps.log 2>&1
r=$?
if grep -x -e 'runtime/cgo' -e 'C' /tmp/deps.log; then echo "ownerstore depends on cgo"; r=1; fi
echo "deps: $(grep -c . /tmp/deps.log) packages"
row "no-cgo: runtime/cgo is not among the dependencies of ownerstore" $r "$s"

s=$(date +%s)
r=0
top=$(grep -c -- '^--- PASS: TestOutcomeVectors ' /tmp/test.log)
sub=$(grep -c -- '--- PASS: TestOutcomeVectors/' /tmp/test.log)
echo "outcome vectors: $top top-level pass, $sub passing runs (13 cases; the 4 cases with an io-at-commit fault run for both landings)"
[ "$top" = 1 ] && [ "$sub" = 17 ] || r=1
grep -q -- '--- PASS: TestRowsMapping ' /tmp/test.log || r=1
row "vectors: TestOutcomeVectors checks all 34 steps, TestRowsMapping holds" $r "$s"

# rowcheck CHECK TEST: the required check of rows.tsv passes through the test it is mapped to
rowcheck() {
	s=$(date +%s)
	grep -q -- "--- PASS: $2 " /tmp/test.log
	row "rows.tsv $1 -> $2" $? "$s"
}
rowcheck settings_equal_class_d1 TestClassReadBack/D1
rowcheck settings_equal_class_d2 TestClassReadBack/D2
rowcheck settings_mismatch_refuses TestClassPlant
rowcheck declared_class_reported TestDeclaredClassReported
rowcheck d1_survives_kill9 TestKillKeepsWrite/D1
rowcheck d2_ack_after_sync TestD2NoAckAfterFailedSync
rowcheck open_missing_refuses TestOpenMissing
rowcheck open_does_not_migrate TestOpenDoesNotMigrate
rowcheck create_failed_leaves_none TestCreateFailedLeavesNone
rowcheck second_writer_refused TestSecondOwner
rowcheck recovery_reports_position TestRecoveryReport
rowcheck recovery_torn_tail_discarded TestRecoveryTornTail
rowcheck seq_no_gap_10000 TestSeqContiguous
rowcheck feed_row_in_transaction TestFeedRowInTransaction
rowcheck cursor_below_retention_gap TestCursorGap
rowcheck cursor_wrong_lineage TestCursorWrongLineage

# mutant NAME FILE FROM TO TESTS FAIL: a copy of the tree with one line of FILE changed must turn the tests red, the
# line FAIL ("--- FAIL: ...") must be among the output, and the change must be exactly one line.
mutant() {
	s=$(date +%s)
	rm -rf /tmp/mut
	mkdir /tmp/mut
	tar -cf - --exclude=./inputs . | tar -xf - -C /tmp/mut
	sed -i "s/$3/$4/" "/tmp/mut/$2"
	d=$(diff "$2" "/tmp/mut/$2" | grep -c '^>')
	(cd /tmp/mut && CGO_ENABLED=0 go test -count=1 -timeout 8m -v -run "^($5)\$" ./ownerstore/) >/tmp/mutant.log 2>&1
	rc=$?
	grep -E '^(--- FAIL|ok|FAIL)|_test.go:[0-9]+:|\[build failed\]|^# ' /tmp/mutant.log | cut -c1-260 | head -8
	if [ "$d" = 1 ] && [ $rc != 0 ] && grep -q -- "--- FAIL: $6" /tmp/mutant.log && ! grep -q -e '\[build failed\]' -e '^# ' /tmp/mutant.log; then
		row "$1: the planted fault in $2 turns $6 red" 0 "$s"
	else
		echo "mutant: lines changed $d, go test rc $rc"
		row "$1: the planted fault in $2 turns $6 red" 1 "$s"
	fi
	rm -rf /tmp/mut
}
O='TestOutcomeVectors|TestClassify'
mutant mutant-absent ownerstore/write.go 'ON CONFLICT (id) DO NOTHING' 'ON CONFLICT (id) DO UPDATE SET value = excluded.value' "$O" TestOutcomeVectors/create_twice
mutant mutant-revision ownerstore/write.go 'AND rev = ?3' 'AND ?3 = ?3' "$O" TestOutcomeVectors/stale_update
mutant mutant-bound ownerstore/write.go 'len(value) > MaxValue' 'len(value) > MaxValue+1' "$O" TestOutcomeVectors/oversized
mutant mutant-commit-io ownerstore/classify.go 'outcome: Unknown, poison: true' 'outcome: Refused, poison: true' "$O" TestOutcomeVectors/io_commit_readback
mutant mutant-poison ownerstore/store.go 's.poison = cause' '_ = cause' "$O" TestOutcomeVectors/poisoned_handle
mutant mutant-reopen ownerstore/store.go 'if s.poisoned() == nil \&\& s.r != nil {' 'if s.r != nil {' "$O" TestOutcomeVectors/poisoned_handle
mutant mutant-settings ownerstore/store.go 'if gotSync != wantSync {' 'if false {' TestClassPlant TestClassPlant
mutant mutant-no-create ownerstore/store.go 'openMode = "rw"' 'openMode = "rwc"' TestOpenMissing TestOpenMissing
mutant mutant-owner-lock ownerstore/store.go 'lk, err := lockFile(abs + ".lock")' 'lk, _ := lockFile(abs + ".lock")' TestSecondOwner TestSecondOwner
mutant mutant-after ownerstore/read.go 'WHERE id > ?1 ORDER' 'WHERE id >= ?1 ORDER' TestScanPaging TestScanPaging
mutant mutant-seq ownerstore/write.go 'SET seq = seq + 1' 'SET seq = seq + 2' TestSeqContiguous TestSeqContiguous

echo "total $(($(date +%s) - t0)) s"
if [ $fails = 0 ]; then echo "RESULT PASS"; exit 0; fi
echo "RESULT FAIL ($fails rows)"
exit 1
