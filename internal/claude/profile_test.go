package claude

import (
	"crypto/sha256"
	"encoding/hex"
)

func profileForTest(specs []patchSpec, disk []byte) compatibilityProfile {
	sum := sha256.Sum256(disk)
	return compatibilityProfile{
		version:          "test",
		packageNames:     []string{"test"},
		executableSHA256: hex.EncodeToString(sum[:]),
		patchSpecs:       specs,
	}
}
