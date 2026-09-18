package cmd_test

import (
	"testing"

	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

func Test_Harness_Setup_HarnessCatalogUsesEveryRouteInOrder(t *testing.T) {
	routes, endpoint, err := internalcmd.HarnessCatalogForTest("https://api.baseten.co", []internalcmd.RouteRecordForTest{
		{Name: "acme/primary", DisplayName: "Engineering", InvokeUrl: "https://coding.baseten.co"},
		{Name: "acme/provider", DisplayName: "Provider", InvokeUrl: "https://coding.baseten.co"},
	})
	require.NoError(t, err)
	require.Equal(t, "https://coding.baseten.co", endpoint)
	require.Len(t, routes, 2)
	require.Equal(t, "acme/primary", routes[0].Name)
	require.Equal(t, "acme/provider", routes[1].Name)
	require.Equal(t, "Provider", routes[1].DisplayName)
}

func Test_Harness_Setup_HarnessCatalogRejectsUntrustedInvokeURL(t *testing.T) {
	_, _, err := internalcmd.HarnessCatalogForTest("https://api.baseten.co", []internalcmd.RouteRecordForTest{{InvokeUrl: "http://127.0.0.1:1234"}})
	require.ErrorContains(t, err, "unsupported invoke URL")
	_, _, err = internalcmd.HarnessCatalogForTest("https://api.baseten.co", []internalcmd.RouteRecordForTest{{InvokeUrl: "https://example.com"}})
	require.ErrorContains(t, err, "unsupported invoke URL")
}
