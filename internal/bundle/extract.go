package bundle

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extractTarGz(r io.Reader, dst string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		name := hdr.Name
		name = strings.TrimPrefix(name, "./")
		name = strings.TrimPrefix(name, ".\\")

		if name == "" || name == "." {
			continue
		}
		if filepath.IsAbs(name) || strings.Contains(name, "..") {
			continue
		}

		target := filepath.Join(dst, name)

		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		absDst, err := filepath.Abs(dst)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(absTarget, absDst+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(f, tr, hdr.Size); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Skip symlinks for security
			continue
		}
	}
	return nil
}
