module github.com/ttab/rpcfixture

// The fixture for a repository that has not moved to the fleet's current Go
// floor. Generation must not depend on which toolchain the repository or the
// machine is on, and TestTwirpOutputIsToolchainIndependent generates it under
// two of them.
go 1.26.5
