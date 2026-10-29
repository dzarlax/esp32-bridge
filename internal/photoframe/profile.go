package photoframe

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
)

// The complete 480-byte CC0 sRGB profile shipped by libvips 8.17.3. Matching
// the entire profile avoids trusting a description string such as "sRGB".
// https://github.com/libvips/libvips/blob/v8.17.3/libvips/colour/profiles/sRGB.icm
const libvipsSRGBHash = "c56e1685d888f5edb92fe07f2750f387f8fe8e91b32ff8fb0b56bfbbb9458353"

const libvipsP3Hash = "231752984cd4a5278e1b8d2390fe496767d4511fc81f54e1a5c69ae9ab4c42b5"

func jpegProfile(data []byte) (string, bool) {
	var parts [256][]byte
	total, count, size := 0, 0, 0
	for p := 2; p < len(data); {
		if data[p] != 0xff {
			return "", false
		}
		for p < len(data) && data[p] == 0xff {
			p++
		}
		if p >= len(data) {
			return "", false
		}
		marker := data[p]
		p++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if p+2 > len(data) {
			return "", false
		}
		n := int(binary.BigEndian.Uint16(data[p : p+2]))
		if n < 2 || n > len(data)-p {
			return "", false
		}
		segment := data[p+2 : p+n]
		p += n
		if marker != 0xe2 || !bytes.HasPrefix(segment, []byte("ICC_PROFILE\x00")) {
			continue
		}
		if len(segment) < 14 {
			return "", false
		}
		seq, declared := int(segment[12]), int(segment[13])
		if declared == 0 || seq == 0 || seq > declared || parts[seq] != nil || total != 0 && total != declared {
			return "", false
		}
		total = declared
		count++
		size += len(segment) - 14
		if size > 480 {
			return "", false
		}
		parts[seq] = segment[14:]
	}
	if count == 0 {
		return "", true
	}
	if count != total || size != 480 {
		return "", false
	}
	profile := make([]byte, 0, 480)
	for i := 1; i <= total; i++ {
		profile = append(profile, parts[i]...)
	}
	sum := sha256.Sum256(profile)
	hash := hex.EncodeToString(sum[:])
	return hash, hash == libvipsSRGBHash || hash == libvipsP3Hash
}

// Both pinned libvips profiles use the same sRGB transfer curve. This matrix
// is inverse(sRGB XYZ colorants) * P3 XYZ colorants, using their D50-adapted
// ICC rXYZ/gXYZ/bXYZ tags. Unsupported profiles never reach this conversion.
func p3ToSRGB(r, g, b uint32) (uint32, uint32, uint32) {
	decode := func(v uint32) float64 {
		x := float64(v) / 65535
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	encode := func(x float64) uint32 {
		if x <= 0 {
			return 0
		}
		if x >= 1 {
			return 65535
		}
		if x <= 0.0031308 {
			x *= 12.92
		} else {
			x = 1.055*math.Pow(x, 1/2.4) - 0.055
		}
		return uint32(math.Round(x * 65535))
	}
	lr, lg, lb := decode(r), decode(g), decode(b)
	return encode(1.224348539059528*lr - 0.22481088635141966*lg + 0.00046234729189154056*lb),
		encode(-0.04191732251492083*lr + 1.0419719857245477*lg - 0.00005466320962680543*lb),
		encode(-0.018139341567139082*lr - 0.07872153235259957*lg + 1.0968608739197385*lb)
}
