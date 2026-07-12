package ticketbackend

import "os"

func envGet(k string) string { return os.Getenv(k) }
