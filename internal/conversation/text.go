package conversation

// Truncate limits text to at most limit bytes, backing off to a valid UTF-8
// boundary, and marks that something was left out. The marker matters: a
// silently shortened output reads as complete, which is the one thing a
// truncated protocol payload must not do.
func Truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && (s[limit]&0xc0) == 0x80 {
		limit--
	}
	return s[:limit] + "\n… Output truncated"
}
