package delivery

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLocalGitAdapterReadsActualCommitAndBranch(t *testing.T) {
	path := t.TempDir()
	ctx := context.Background()
	run := func(args ...string) {
		out, err := localGitCommand(ctx, path, args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "--initial-branch=main")
	run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fix #WI-139 #WI-999")
	run("branch", "feature/#WI-139")
	objects, err := ReadLocalGit(ctx, path)
	require.NoError(t, err)
	commits, branches := 0, 0
	for _, object := range objects {
		switch object.Kind {
		case "COMMIT":
			commits++
			require.True(t, commitRef.MatchString(object.Ref))
			require.Len(t, ParseCodeReferences(object.Title), 2)
		case "BRANCH":
			branches++
		}
	}
	require.Equal(t, 1, commits)
	require.Equal(t, 2, branches)
	_, err = ReadLocalGit(ctx, "relative")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = ReadLocalGit(ctx, t.TempDir())
	require.Error(t, err)
}
func TestLocalGitOutputBound(t *testing.T) {
	buffer := &codeGitBuffer{limit: 3}
	n, err := buffer.Write([]byte("abc"))
	require.NoError(t, err)
	require.Equal(t, 3, n)
	_, err = buffer.Write([]byte("d"))
	require.ErrorIs(t, err, ErrUnavailable)
	require.Equal(t, "abc", buffer.String())
}
