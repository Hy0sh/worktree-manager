package compose

import (
	"os"

	"gopkg.in/yaml.v3"
)

// ServiceImage returns the image a service runs, reading the project's merged
// compose configuration: an override redefining the service wins.
func ServiceImage(dir, service string) (string, bool) {
	files, err := Files(dir)
	if err != nil {
		return "", false
	}
	for i := len(files) - 1; i >= 0; i-- {
		if img, ok := serviceImageOf(files[i], service); ok {
			return img, true
		}
	}
	return "", false
}

func serviceImageOf(path, service string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false
	}
	img := mapValue(mapValue(servicesNode(&doc), service), "image")
	if img == nil || img.Kind != yaml.ScalarNode || img.Value == "" {
		return "", false
	}
	return img.Value, true
}
