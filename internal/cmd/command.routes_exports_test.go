package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

// Test-only access to the Routes transport helpers.
type RouteRecordForTest = managementapi.Route

var ReadRoutesForTest = readRoutes
var CreateRoutesKeyForTest = createRoutesKey
