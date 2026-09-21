package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandVolumeSync = Command{
	Name:    "sync",
	Summary: "Materialize remote content into a volume (PRE-RELEASE)",
	Description: volumePreRelease +
		"Manage durable asynchronous jobs that copy one supported remote source into one BDN volume. " +
		"A successful sync publishes an immutable volume version only after transfer and verification complete.",
	Children: []Command{
		{
			Name:    "start",
			Summary: "Start a remote volume sync (PRE-RELEASE)",
			Description: volumePreRelease +
				"Starts one durable asynchronous transfer from --source into --destination. The source URI " +
				"scheme selects the provider: hf://, s3://, gs://, azure://, r2://, cw://, or bt://. " +
				"Repeat --include and --exclude to filter source-relative paths.\n\n" +
				"Remote credentials must already be stored as a Baseten secret or made available through " +
				"AWS AssumeRole. The CLI infers the authentication method from the --auth-* flags and never " +
				"accepts plaintext credentials. Omit every authentication flag for a public source.\n\n" +
				"By default the command returns as soon as the server creates the job. Pass --wait to poll " +
				"until it is READY, FAILED, or CANCELED. Progress is written to stderr and the final result " +
				"to stdout.",
			Flags: VolumeSyncStartFlags{},
			Output: &CommandOutput[managementapi.VolumeSync]{
				TextDescription: "One field per line describing the sync. With --wait, the result is the " +
					"terminal state and a successful result includes its immutable version ref.",
				Examples: []CommandExample{
					{
						Description: "Start a sync from a public Hugging Face repository.",
						CommandLines: []string{
							"baseten volume sync start",
							"--source hf://<organization>/<repository>",
							"--destination bdn:<namespace>/<volume>:<tag>",
						},
					},
					{
						Description: "Start an S3 sync using AWS AssumeRole and wait for it to finish.",
						CommandLines: []string{
							"baseten volume sync start",
							"--source s3://<bucket>/<prefix>",
							"--destination bdn:<namespace>/<volume>:<tag>",
							"--auth-aws-assume-role-arn <role-arn>",
							"--auth-aws-assume-role-region <region>",
							"--wait",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Start a sync and print its operation ID.",
					CommandLines: []string{
						"baseten volume sync start",
						"--source hf://<organization>/<repository>",
						"--destination bdn:<namespace>/<volume>",
						"--jq '.sync_id'",
					},
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a remote volume sync (PRE-RELEASE)",
			Description: volumePreRelease +
				"Retrieves the current state of one sync. A READY sync includes the immutable version ref " +
				"produced by the job; a FAILED sync includes a stable error code and redacted message.",
			Flags: VolumeSyncIDFlags{},
			Output: &CommandOutput[managementapi.VolumeSync]{
				TextDescription: "One field per line describing the sync and, when available, its result or error.",
				Examples: []CommandExample{{
					Description: "Inspect a sync.",
					Command:     "baseten volume sync describe --volume-sync-id <volume-sync-id>",
				}},
				JQExample: CommandExample{
					Description: "Print the sync status.",
					Command:     "baseten volume sync describe --volume-sync-id <volume-sync-id> --jq '.status'",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List remote volume syncs (PRE-RELEASE)",
			Description: volumePreRelease +
				"Lists every sync visible in the active workspace, newest first. Pass --destination to " +
				"match one exact destination ref. The CLI follows every server page.",
			Flags: VolumeSyncListFlags{},
			Output: &CommandOutput[VolumeSyncList]{
				TextDescription: "Table with columns: ID, STATUS, SOURCE, DESTINATION, SIZE, CREATED, COMPLETED. " +
					"Prints \"No volume syncs found.\" to stderr when the list is empty.",
				Examples: []CommandExample{
					{
						Description: "List visible syncs.",
						Command:     "baseten volume sync list",
					},
					{
						Description: "List syncs for one exact destination.",
						Command:     "baseten volume sync list --destination bdn:<namespace>/<volume>:<tag>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the IDs of failed syncs.",
					Command:     "baseten volume sync list --jq '.items[] | select(.status == \"FAILED\") | .sync_id'",
				},
			},
		},
		{
			Name:    "cancel",
			Summary: "Cancel a remote volume sync (PRE-RELEASE)",
			Description: volumePreRelease +
				"Requests cancellation of one pending or syncing job. Cancellation is idempotent: a " +
				"terminal sync is returned unchanged, and an artifact already published by a READY sync " +
				"is not removed.",
			Flags: VolumeSyncIDFlags{},
			Output: &CommandOutput[managementapi.VolumeSync]{
				TextDescription: "One field per line describing the sync after the cancellation request.",
				Examples: []CommandExample{{
					Description: "Request cancellation of a sync.",
					Command:     "baseten volume sync cancel --volume-sync-id <volume-sync-id>",
				}},
				JQExample: CommandExample{
					Description: "Request cancellation and print the resulting status.",
					Command:     "baseten volume sync cancel --volume-sync-id <volume-sync-id> --jq '.status'",
				},
			},
		},
	},
}

type VolumeSyncStartFlags struct {
	CommandFlags

	Source      string   `flag:"source" desc:"Remote source URI. Supported schemes: hf://, s3://, gs://, azure://, r2://, cw://, and bt://." required:"true"`
	Destination string   `flag:"destination" desc:"Destination ref as bdn:<namespace>/<volume>, with an optional :<tag>." required:"true"`
	Include     []string `flag:"include" desc:"Glob selecting source-relative files to include. May be repeated."`
	Exclude     []string `flag:"exclude" desc:"Glob selecting source-relative files to exclude. May be repeated."`

	AuthSecretName          string `flag:"auth-secret-name" desc:"Baseten secret containing source credentials. Supported by hf://, s3://, gs://, azure://, r2://, and cw:// sources." group:"authentication"`
	AuthAWSAssumeRoleARN    string `flag:"auth-aws-assume-role-arn" desc:"AWS IAM role ARN for an s3:// source. Requires --auth-aws-assume-role-region." group:"authentication"`
	AuthAWSAssumeRoleRegion string `flag:"auth-aws-assume-role-region" desc:"AWS region for the AssumeRole session. Requires --auth-aws-assume-role-arn." group:"authentication"`

	Wait bool `flag:"wait" desc:"Poll until the sync reaches READY, FAILED, or CANCELED. Does not cancel the server-side job if interrupted."`
}

type VolumeSyncIDFlags struct {
	CommandFlags

	VolumeSyncID string `flag:"volume-sync-id" desc:"ID of the volume sync operation." required:"true"`
}

type VolumeSyncListFlags struct {
	CommandFlags

	Destination string `flag:"destination" desc:"Only return syncs whose destination exactly matches this ref."`
}

type VolumeSyncList struct {
	Items []managementapi.VolumeSync `json:"items"`
}
