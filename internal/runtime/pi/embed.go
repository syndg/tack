package pi

import "embed"

//go:embed extension
var extensionFS embed.FS

// ExtensionFiles returns the embedded extension files as a map of path → content.
// Used by Runtime.Spawn() to upload the extension to the sandbox.
func ExtensionFiles() map[string][]byte {
	files := make(map[string][]byte)
	entries, err := extensionFS.ReadDir("extension")
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := extensionFS.ReadFile("extension/" + entry.Name())
		if err != nil {
			continue
		}
		files[entry.Name()] = data
	}
	return files
}
