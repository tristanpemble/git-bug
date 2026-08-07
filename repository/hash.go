package repository

const idLengthSHA1 = 40
const idLengthSHA256 = 64

// Hash is a git hash
type Hash string

func (h Hash) String() string {
	return string(h)
}

// IsValid tell if the hash is valid
func (h *Hash) IsValid() bool {
	// Support for both sha1 and sha256 git hashes
	if len(*h) != idLengthSHA1 && len(*h) != idLengthSHA256 {
		return false
	}
	for _, r := range *h {
		if (r < 'a' || r > 'f') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
