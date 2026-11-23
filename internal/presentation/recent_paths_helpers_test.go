package presentation

// test file helpers shared by the recent-paths fixtures (keeps the
// test file free of extra imports at top level).
import "os"

func osReadFileForTest(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func osWriteFileForTest(path string, b []byte, mode os.FileMode) error {
	return os.WriteFile(path, b, mode)
}
