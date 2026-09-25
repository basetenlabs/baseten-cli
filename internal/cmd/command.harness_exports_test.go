package cmd

// Test-only access to pure helpers; these names are absent from the CLI build.

type HarnessRouteRecordForTest = harnessRouteRecord

var HarnessCatalogForTest = harnessCatalog
var ReadHarnessMCPServersForTest = readMCPServers
var HarnessMCPTokenEnvVarForTest = harnessMCPTokenEnvVar
var ResolveHarnessMCPServerTokensForTest = resolveHarnessMCPServerTokens
