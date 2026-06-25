package output

import "io"

func Text(w io.Writer, content string) error {
	if content == "" {
		return nil
	}
	if content[len(content)-1] != '\n' {
		content += "\n"
	}
	_, err := io.WriteString(w, content)
	return err
}
