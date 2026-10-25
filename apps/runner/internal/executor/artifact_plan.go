package executor

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

type PlannedArtifact struct {
	CriterionID string `json:"criterion_id"`
	Path        string `json:"path"`
	Note        string `json:"note"`
}
type CapturedArtifact struct {
	CriterionID string `json:"criterion_id"`
	Name        string `json:"name"`
	Note        string `json:"note"`
	Data        []byte `json:"data"`
}

// Read the immutable Git blob, never a mutable filesystem path. ls-tree requires
// a regular file: symlinks and submodules cannot escape the isolated worktree.
func capturePlannedArtifacts(ctx context.Context, p Plan, head string) ([]CapturedArtifact, error) {
	if len(p.Artifacts) > 50 {
		return nil, ErrUnsupported
	}
	out := []CapturedArtifact{}
	for _, a := range p.Artifacts {
		id, e := strconv.ParseInt(a.CriterionID, 10, 64)
		if e != nil || id <= 0 || !fs.ValidPath(a.Path) || a.Path == "." || len(a.Path) > 512 || len(path.Base(a.Path)) > 200 || strings.ContainsAny(a.Path, "\\:\x00") || strings.EqualFold(strings.Split(a.Path, "/")[0], ".git") || a.Note == "" || len(a.Note) > 4000 {
			return nil, ErrUnsupported
		}
		entry, err := workspaceGit(ctx, p.Workdir, "ls-tree", "-z", head, "--", ":(literal)"+a.Path)
		if err != nil {
			return nil, err
		}
		metadata, file, ok := strings.Cut(strings.TrimSuffix(string(entry), "\x00"), "\t")
		parts := strings.Fields(metadata)
		if !ok || file != a.Path || len(parts) != 3 || (parts[0] != "100644" && parts[0] != "100755") || parts[1] != "blob" {
			return nil, fmt.Errorf("%w: declared artifact is not a committed regular file", ErrRuntimeFailed)
		}
		data, err := workspaceGit(ctx, p.Workdir, "cat-file", "blob", parts[2])
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("%w: declared artifact is empty", ErrRuntimeFailed)
		}
		out = append(out, CapturedArtifact{CriterionID: a.CriterionID, Name: path.Base(a.Path), Note: a.Note + " Source: final execution commit; criterion association is not a verification verdict.", Data: data})
	}
	return out, nil
}
