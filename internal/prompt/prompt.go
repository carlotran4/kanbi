package prompt

import "fmt"

func Render(displayID, title, body string) string {
	if body == "" {
		body = title
	}
	return fmt.Sprintf("# %s: %s\n\n%s", displayID, title, body)
}
