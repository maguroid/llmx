package output

import "io"

func Text(w io.Writer, content string) error {
	_, err := io.WriteString(w, content)
	return err
}
