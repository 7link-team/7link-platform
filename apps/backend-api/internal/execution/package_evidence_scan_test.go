package execution

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"os"
	"strings"
	"testing"
)

func TestPackageEvidenceScannerInspectsActualBytesAndZipEntries(t *testing.T) {
	scan := func(name, mime string, raw []byte) PackageEvidenceAssessment {
		sum := sha256.Sum256(raw)
		out, err := ScanPackageEvidence(t.Context(), name, mime, hex.EncodeToString(sum[:]), raw)
		require.NoError(t, err)
		require.Equal(t, "execution:source_bytes", out.Provenance)
		return out
	}
	require.True(t, scan("report.txt", "text/plain", []byte("password=actual-secret")).SecretFlagged)
	require.False(t, scan("report.txt", "text/plain", []byte("actual passing report")).SecretFlagged)
	var b bytes.Buffer
	writer := zip.NewWriter(&b)
	entry, err := writer.Create(".env")
	require.NoError(t, err)
	_, err = entry.Write([]byte("actual-secret"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	out := scan("evidence.zip", "application/zip", b.Bytes())
	require.True(t, out.SecretScanned)
	require.True(t, out.SecretFlagged)
	b.Reset()
	writer = zip.NewWriter(&b)
	entry, err = writer.Create("inner.zip")
	require.NoError(t, err)
	_, err = entry.Write([]byte("opaque"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.False(t, scan("evidence.zip", "application/zip", b.Bytes()).SecretScanned)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	raw := []byte("report")
	sum := sha256.Sum256(raw)
	_, err = ScanPackageEvidence(ctx, "report.txt", "text/plain", hex.EncodeToString(sum[:]), raw)
	require.ErrorIs(t, err, context.Canceled)
	_, err = ScanPackageEvidence(t.Context(), "report.txt", "text/plain", "wrong", raw)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestPackageEvidenceIdentityTermsUseScopedPersistedExecution(t *testing.T) {
	f := executionHarness(t)
	created := f.create(t)
	lookup := f.m.machines.ByIDOn
	f.m.machines.ByIDOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.MachineFact, error) {
		machine, err := lookup(ctx, tx, tenant, id)
		machine.Name = "CI"
		return machine, err
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		terms, err := f.m.packageEvidenceIdentityTermsOn(f.human, tx, 7, 9, []guid.ID{created.ID})
		require.NoError(t, err)
		require.Contains(t, terms, "CI")
		_, err = f.m.packageEvidenceIdentityTermsOn(f.human, tx, 7, 10, []guid.ID{created.ID})
		require.Error(t, err)
		return nil
	}))
}

func TestPackageEvidenceScannerClassifiesSecretNamesEntropyAndOpaqueArchives(t *testing.T) {
	for _, name := range []string{"keys/private.pem", "config/.npmrc", "config/.pypirc", "kubeconfig", "id_ed25519.pub", "credentials.local"} {
		out, e := ScanPackageEvidence(t.Context(), name, "text/plain", hash([]byte("data")), []byte("data"))
		require.NoError(t, e)
		require.True(t, out.SecretScanned)
		require.True(t, out.SecretFlagged)
	}
	for _, entry := range []struct {
		data    string
		flagged bool
	}{{"opaque=abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", true}, {"opaque=" + strings.Repeat("a", 64), false}} {
		out, e := ScanPackageEvidence(t.Context(), "settings.txt", "text/plain", hash([]byte(entry.data)), []byte(entry.data))
		require.NoError(t, e)
		require.Equal(t, entry.flagged, out.SecretFlagged)
		require.True(t, out.SecretScanned)
	}
	for _, kind := range []string{"directory", "symlink", "nested_magic", "too_many", "bad_crc", "invalid_zip"} {
		t.Run(kind, func(t *testing.T) {
			var data bytes.Buffer
			w := zip.NewWriter(&data)
			if kind == "too_many" {
				for i := 0; i < 501; i++ {
					_, e := w.Create(fmt.Sprintf("file_%d.txt", i))
					require.NoError(t, e)
				}
			} else if kind != "invalid_zip" {
				header := &zip.FileHeader{Name: "report.txt", Method: zip.Store}
				content := []byte("actual report")
				switch kind {
				case "directory":
					header.Name = "folder/"
					header.SetMode(os.ModeDir | 0700)
				case "symlink":
					header.SetMode(os.ModeSymlink | 0700)
				case "nested_magic":
					content = []byte{'P', 'K', 3, 4, 'x'}
				}
				f, e := w.CreateHeader(header)
				require.NoError(t, e)
				if kind != "directory" {
					_, e = f.Write(content)
					require.NoError(t, e)
				}
			}
			require.NoError(t, w.Close())
			raw := append([]byte(nil), data.Bytes()...)
			if kind == "bad_crc" {
				at := bytes.Index(raw, []byte("actual report"))
				require.NotEqual(t, -1, at)
				raw[at] = 'X'
			}
			if kind == "invalid_zip" {
				raw = []byte("not a zip")
			}
			out, e := ScanPackageEvidence(t.Context(), "report.zip", " application/zip; charset=binary ", hash(raw), raw)
			switch kind {
			case "bad_crc", "invalid_zip", "too_many":
				require.Error(t, e)
				require.False(t, out.SecretScanned)
			case "symlink", "nested_magic":
				require.NoError(t, e)
				require.False(t, out.SecretScanned)
			default:
				require.NoError(t, e)
				require.True(t, out.SecretScanned)
			}
		})
	}
}

func TestProvidePackageEvidenceScannerRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.providePackageEvidenceScanner(s))
	require.ErrorIs(t, f.m.providePackageEvidenceScanner(s), module.ErrServiceDuplicate)
}
