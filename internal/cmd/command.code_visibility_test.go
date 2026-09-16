package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	public "github.com/basetenlabs/baseten-cli/cmd"
	mango "github.com/muesli/mango-cobra"
	"github.com/muesli/roff"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/stretchr/testify/require"
)

func Test_Code_GeneratedDocumentationAndStaticCompletion(t *testing.T) {
	root := &cobra.Command{Use: "baseten"}
	for _, child := range public.Root.Children {
		root.AddCommand(buildCommand(child, "", &ExecuteOptions{}))
	}
	dir := t.TempDir()
	require.NoError(t, doc.GenMarkdownTree(root, dir))
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, file := range files {
		require.NotContains(t, file.Name(), "baseten_code")
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		require.NoError(t, err)
		require.NotContains(t, string(data), "Configure Baseten Code")
	}
	page, err := mango.NewManPage(1, root)
	require.NoError(t, err)
	require.NotContains(t, page.Build(roff.NewDocument()), "Configure Baseten Code")
	require.NotContains(t, page.Build(roff.NewDocument()), "Code key metadata")
	var b bytes.Buffer
	require.NoError(t, root.GenBashCompletion(&b))
	require.NotContains(t, b.String(), "_baseten_code")
}
