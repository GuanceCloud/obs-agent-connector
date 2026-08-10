package install

func ReadRuntimeConfig(path string) (map[string]any, bool, error) {
	return readJSONObjectIfExists(path)
}

func MergeRuntimeConfig(current map[string]any, options CodexOptions, existed bool) (map[string]any, error) {
	return mergeCodexGTraceConfig(current, options, existed)
}

func WriteRuntimeConfig(path string, value map[string]any) error {
	return writeJSONAtomic(path, value)
}
