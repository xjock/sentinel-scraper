package bundle

func EnsureExtracted() (string, error) {
	return ensureExtracted()
}

func ToolPath(name string) (string, error) {
	return toolPath(name)
}

func ProjDataPath() (string, error) {
	return projDataPath()
}
