package source

func extractionArgs(url string) []string {
	return []string{"--ignore-config", "--no-playlist", "--no-progress", "--no-warnings",
		"--retries", "0", "--fragment-retries", "0", "--extractor-retries", "0", "--file-access-retries", "0",
		"--socket-timeout", "15", "-f", "bestaudio[ext=webm]/bestaudio", "-o", "-", "--", url}
}
