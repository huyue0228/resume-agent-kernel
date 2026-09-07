package pipeline

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	p "resume-agent-kernel/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestDocumentCommandOutputLimitCannotBeBypassedByCopy(t *testing.T) {
	var output boundedDocumentOutput
	if _, err := io.Copy(&output, strings.NewReader(strings.Repeat("x", (1<<20)+1))); err == nil {
		t.Fatal("oversized parser output accepted")
	}
	if len(output.Bytes()) > 1<<20 {
		t.Fatal("document output budget exceeded")
	}
}

func signedArtifact(path string, data []byte) p.ArtifactRefV1 {
	hash := sha256.Sum256(data)
	a := p.ArtifactRefV1{Path: path, Checksum: hex.EncodeToString(hash[:]), SizeBytes: int64(len(data)), MediaType: "application/pdf", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	mac := hmac.New(sha256.New, []byte("key"))
	fmt.Fprintf(mac, "%s\n%s\n%d\n%d", a.Path, a.Checksum, a.SizeBytes, a.ExpiresAt)
	a.Signature = hex.EncodeToString(mac.Sum(nil))
	return a
}
func TestDocumentReferencesRejectTraversalTamperExpiryAndSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	data := []byte("%PDF-invalid")
	os.WriteFile(filepath.Join(root, "resume.pdf"), data, 0600)
	os.WriteFile(filepath.Join(outside, "private.pdf"), data, 0600)
	os.Symlink(filepath.Join(outside, "private.pdf"), filepath.Join(root, "link.pdf"))
	d := LocalDocuments{Root: root, SigningKey: "key"}
	expired := signedArtifact("resume.pdf", data)
	expired.ExpiresAt = 1
	tampered := signedArtifact("resume.pdf", data)
	tampered.Checksum = "other"
	for _, a := range []p.ArtifactRefV1{signedArtifact("../private.pdf", data), signedArtifact("link.pdf", data), expired, tampered, {InlineText: "untrusted inline PDF"}} {
		if _, err := d.Read(context.Background(), a, 10); err == nil {
			t.Fatalf("invalid document reference accepted: %s", a.Path)
		}
	}
}
