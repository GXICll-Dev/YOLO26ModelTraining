package license

import "crypto/md5"

func legacyMD5(data []byte) [16]byte {
	return md5.Sum(data)
}
