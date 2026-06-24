package ssh

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHostname(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		want    hostname
		wantErr bool
	}{
		{
			name: "model without replica",
			host: "model-abc-def.ssh.baseten.co",
			want: hostname{kind: workloadModel, id: "abc", deploymentID: "def"},
		},
		{
			name: "model with replica",
			host: "model-abc-def-7.ssh.baseten.co",
			want: hostname{kind: workloadModel, id: "abc", deploymentID: "def", replica: "7"},
		},
		{
			name: "model replica may contain dashes",
			host: "model-abc-def-replica-3.ssh.baseten.co",
			want: hostname{kind: workloadModel, id: "abc", deploymentID: "def", replica: "replica-3"},
		},
		{
			name: "model without domain",
			host: "model-abc-def",
			want: hostname{kind: workloadModel, id: "abc", deploymentID: "def"},
		},
		{
			name: "training job",
			host: "training-job-job123-0.ssh.baseten.co",
			want: hostname{kind: workloadTraining, id: "job123", replica: "0"},
		},
		{
			name: "training job id may contain dashes",
			host: "training-job-my-job-2.ssh.baseten.co",
			want: hostname{kind: workloadTraining, id: "my-job", replica: "2"},
		},
		{name: "unknown prefix", host: "foo-bar.ssh.baseten.co", wantErr: true},
		{name: "model missing deployment", host: "model-abc", wantErr: true},
		{name: "model empty deployment", host: "model-abc-", wantErr: true},
		{name: "model empty replica", host: "model-abc-def-", wantErr: true},
		{name: "training missing node", host: "training-job-job", wantErr: true},
		{name: "training non-numeric node", host: "training-job-job-x", wantErr: true},
		{name: "training empty job id", host: "training-job--0", wantErr: true},
		{name: "model component with forward slash", host: "model-a-b/c", wantErr: true},
		{name: "model component with backslash", host: `model-a-b\c`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHostname(tc.host)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRelay_InvalidProxyAddress(t *testing.T) {
	err := relay(context.Background(), "no-port", "jwt", strings.NewReader(""), io.Discard)
	require.ErrorContains(t, err, "invalid proxy address")
}

func TestRelay_SendsFramedJWTAndReturnsOnSuccess(t *testing.T) {
	t.Setenv("BASETEN_SSH_PROXY_INSECURE", "1")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- ""
			return
		}
		defer conn.Close()
		jwt, err := readFrame(conn)
		if err != nil {
			got <- ""
			return
		}
		// Accept the connection.
		_, _ = conn.Write([]byte{0x00})
		got <- jwt
	}()

	err = relay(context.Background(), ln.Addr().String(), "my-jwt", strings.NewReader(""), io.Discard)
	require.NoError(t, err)
	require.Equal(t, "my-jwt", <-got)
}

func TestRelay_RejectedStatusErrors(t *testing.T) {
	t.Setenv("BASETEN_SSH_PROXY_INSECURE", "1")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := readFrame(conn); err != nil {
			return
		}
		// Reject the connection.
		_, _ = conn.Write([]byte{0x01})
	}()

	err = relay(context.Background(), ln.Addr().String(), "my-jwt", strings.NewReader(""), io.Discard)
	require.ErrorContains(t, err, "rejected")
}

// readFrame reads the 4-byte big-endian length prefix and that many JWT bytes
// from conn, mirroring relay's authorization framing.
func readFrame(conn net.Conn) (string, error) {
	var lp [4]byte
	if _, err := io.ReadFull(conn, lp[:]); err != nil {
		return "", err
	}
	buf := make([]byte, binary.BigEndian.Uint32(lp[:]))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
