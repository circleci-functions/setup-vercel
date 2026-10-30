package vercelcli_test

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-vercel/vercelcli"
)

func TestDeploymentRef(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{"url", "https://my-app-abc.vercel.app\n", "https://my-app-abc.vercel.app"},
		{"url after other output", "Uploading\nhttps://my-app-abc.vercel.app", "https://my-app-abc.vercel.app"},
		{"agent json", `{"status":"ok","deployment":{"id":"dpl_1","url":"https://my-app-abc.vercel.app"}}`, "dpl_1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vercelcli.DeploymentRef(tt.stdout)
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(got, tt.want))
		})
	}
}

func TestDeploymentRef_Nothing(t *testing.T) {
	for _, stdout := range []string{"", "\n", `{"status":"error"}`} {
		_, err := vercelcli.DeploymentRef(stdout)
		assert.Check(t, cmp.ErrorContains(err, "printed no deployment URL"), stdout)
	}
}
