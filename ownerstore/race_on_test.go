// SPDX-License-Identifier: MIT

//go:build race

package ownerstore

// feedTxns is the number of transactions of TestSeqContiguous: the contract asks for 10,000; under the race
// detector the pure-Go SQLite is an order of magnitude slower, so the row there runs 1,000.
const feedTxns = 1000
