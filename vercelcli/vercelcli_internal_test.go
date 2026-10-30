package vercelcli

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestBashPath(t *testing.T) {
	// filepath.ToSlash only rewrites the OS's own separator, so a Windows path
	// is only fully converted on Windows; the drive letter is handled anywhere.
	assert.Check(t, cmp.Equal(bashPath("C:/Users/circleci/.cache"), "/c/Users/circleci/.cache"))
	assert.Check(t, cmp.Equal(bashPath("/home/circleci/.cache"), "/home/circleci/.cache"))
}
