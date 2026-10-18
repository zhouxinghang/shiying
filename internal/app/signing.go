package app

// Go port of this repository's Apache-2.0 native Douyin signer:
// src/dtk/signing/native/{abogus,sm3,websign}.py (bdms 1.0.1.19-fix.01).
// This is a compatibility protocol, not a way around access controls.

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
const browserInfo = "1920|947|1920|1032|1920|1032|1920|1080|Win32"

var fieldOrder = []int{34, 44, 56, 61, 73, 29, 70, 45, 35, 49, 38, 66, 51, 68, 28, 48, 64, 47, 30, 71, 26, 55, 31, 69, 59, 40, 62, 63, 27, 72, 41, 74, 57, 52, 42, 39, 33, 67, 53, 43, 65, 46, 36, 24, 60, 32, 79, 80, 84, 85}
var s3 = base64.NewEncoding("ckdp1h4ZKsUB80/Mfvw36XIgR25+WQAlEi7NLboqYTOPuzmFjJnryx9HVGDaStCe")
var s4 = base64.NewEncoding("Dkdpgh2ZmsQB80/MfvV36XI1R45-WUAlEixNLwoqYTOPuzKFjJnry79HbGcaStCe")

func sm3(message []byte) []byte {
	state := [8]uint32{0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600, 0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e}
	data := append(append([]byte{}, message...), 0x80)
	for len(data)%64 != 56 {
		data = append(data, 0)
	}
	data = binary.BigEndian.AppendUint64(data, uint64(len(message))*8)
	rol := bits.RotateLeft32
	for offset := 0; offset < len(data); offset += 64 {
		var w [68]uint32
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(data[offset+i*4:])
		}
		for i := 16; i < 68; i++ {
			x := w[i-16] ^ w[i-9] ^ rol(w[i-3], 15)
			w[i] = x ^ rol(x, 15) ^ rol(x, 23) ^ rol(w[i-13], 7) ^ w[i-6]
		}
		a, b, c, d, e, f, g, h := state[0], state[1], state[2], state[3], state[4], state[5], state[6], state[7]
		for j := 0; j < 64; j++ {
			t := uint32(0x79cc4519)
			ff := a ^ b ^ c
			gg := e ^ f ^ g
			if j >= 16 {
				t = 0x7a879d8a
				ff = (a & b) | (a & c) | (b & c)
				gg = (e & f) | (^e & g)
			}
			ss1 := rol(rol(a, 12)+e+rol(t, j), 7)
			ss2 := ss1 ^ rol(a, 12)
			tt1 := ff + d + ss2 + (w[j] ^ w[j+4])
			tt2 := gg + h + ss1 + w[j]
			d = c
			c = rol(b, 9)
			b = a
			a = tt1
			h = g
			g = rol(f, 19)
			f = e
			e = tt2 ^ rol(tt2, 9) ^ rol(tt2, 17)
		}
		for i, v := range [8]uint32{a, b, c, d, e, f, g, h} {
			state[i] ^= v
		}
	}
	out := make([]byte, 0, 32)
	for _, v := range state {
		out = binary.BigEndian.AppendUint32(out, v)
	}
	return out
}

func sdkCipher(key, data []byte) []byte {
	var box [256]int
	for i := range box {
		box[i] = 255 - i
	}
	j := 0
	for i := range box {
		j = (j*box[i] + j + int(key[i%len(key)])) % 256
		box[i], box[j] = box[j], box[i]
	}
	out := make([]byte, len(data))
	i := 0
	j = 0
	for pos, v := range data {
		i = (i + 1) % 256
		j = (j + box[i]) % 256
		box[i], box[j] = box[j], box[i]
		out[pos] = v ^ byte(box[(box[i]+box[j])%256])
	}
	return out
}

func digestChain(text string) []byte { return sm3(sm3([]byte(text + "dhzx"))) }
func canaryByte(digest []byte, offset int, sentinel, fallback byte) byte {
	for _, v := range digest[offset:] {
		if v != sentinel {
			return v
		}
	}
	return fallback
}
func bogusFields(query, ua string, nowMs int64) [86]byte {
	var f [86]byte
	geometry := []byte(browserInfo)
	tail := []byte(fmt.Sprintf("%d,", (nowMs+3)&255))
	f[24] = 41
	f[26] = byte((nowMs - 1721836800000) / (1000 * 60 * 60 * 24 * 14))
	f[27] = 6
	f[28] = 3
	f[35] = 1
	f[38] = 0x21
	f[44] = 14
	f[66] = 3
	put := func(first, count int, value int64) {
		for i := 0; i < count; i++ {
			f[first+i] = byte(value >> (8 * i))
		}
	}
	put(29, 6, nowMs)
	put(60, 6, nowMs-1)
	put(67, 4, 6241)
	put(71, 4, 6383)
	put(79, 2, int64(len(geometry)))
	put(84, 2, int64(len(tail)))
	queryDigest := digestChain(query)
	bodyDigest := digestChain("")
	uaDigest := sm3([]byte(s3.EncodeToString(sdkCipher([]byte{0, 1, 14}, []byte(strings.TrimSpace(ua))))))
	f[48] = queryDigest[9]
	f[49] = queryDigest[18]
	f[51] = canaryByte(queryDigest, 3, 11, 12)
	f[52] = bodyDigest[10]
	f[53] = bodyDigest[19]
	f[55] = canaryByte(bodyDigest, 4, 8, 9)
	f[56] = uaDigest[11]
	f[57] = uaDigest[21]
	f[59] = canaryByte(uaDigest, 5, 12, 13)
	return f
}

func aBogus(query, ua string, nowMs int64) string {
	// The UI accepts ASCII desktop User-Agents; geometry is the fixed ASCII profile above.
	noise := make([]byte, 1024)
	if _, err := rand.Read(noise); err != nil {
		panic(err)
	}
	cursor := 0
	random := func(limit int) int {
		v := binary.LittleEndian.Uint16(noise[cursor:])
		cursor += 2
		return int(v) * limit / 65536
	}
	pair := func(a, b byte, low, high int) []byte {
		n := random(65535)
		if low < 0 {
			low = n & 255
		}
		if high < 0 {
			high = (n >> 8) & 255
		}
		return []byte{byte(low)&0xaa | a&0x55, byte(low)&0x55 | a&0xaa, byte(high)&0xaa | b&0x55, byte(high)&0x55 | b&0xaa}
	}
	probe := random(240)
	if probe > 109 {
		probe = probe + probe%2 + 1
	}
	tripwire := (random(255) & 0x4d) | 0xb2
	version := append(pair(1, 0, -1, -1), pair(1, 0, probe, tripwire)...)
	fields := bogusFields(query, ua, nowMs)
	checksum := byte(0)
	for _, b := range version {
		checksum ^= b
	}
	body := make([]byte, 0, 110)
	for _, index := range fieldOrder {
		body = append(body, fields[index])
		checksum ^= fields[index]
	}
	body = append(body, []byte(browserInfo)...)
	body = append(body, []byte(fmt.Sprintf("%d,", (nowMs+3)&255))...)
	body = append(body, checksum)
	frame := make([]byte, 0, len(body)*4/3)
	for i := 0; i < len(body); i += 3 {
		if len(body)-i < 3 {
			frame = append(frame, body[i])
			if len(body)-i > 1 && body[i+1] != 0 {
				frame = append(frame, body[i+1])
			}
			break
		}
		a, b, c := body[i], body[i+1], body[i+2]
		n := byte(random(1000))
		frame = append(frame, n&0x91|a&0x6e, n&0x42|b&0xbd, n&0x2c|c&0xd3, a&0x91|b&0x42|c&0x2c)
	}
	base := 210
	uaLower := strings.ToLower(ua)
	for _, family := range []struct {
		name string
		base int
	}{{"edg", 125}, {"huawei", 170}, {"firefox", 40}, {"chrome", 0}, {"safari", 81}} {
		if strings.Contains(uaLower, family.name) {
			base = family.base
			break
		}
	}
	header := pair(3, 82, -1, base+random(40))
	sealed := sdkCipher([]byte{0xd3}, append(version, frame...))
	return s4.EncodeToString(append(header, sealed...))
}

func cookieValues(raw string) map[string]string {
	req := &http.Request{Header: http.Header{"Cookie": []string{raw}}}
	out := map[string]string{}
	for _, c := range req.Cookies() {
		out[c.Name] = c.Value
	}
	return out
}

type queryParam struct{ key, value string }

func signedDetailURL(id, cookie string, nowMs int64) (string, map[string]string) {
	return signedAPIURL("/aweme/v1/web/aweme/detail/", []queryParam{{"aweme_id", id}}, cookie, nowMs)
}
func signedAPIURL(endpoint string, extra []queryParam, cookie string, nowMs int64) (string, map[string]string) {
	params := url.Values{
		"device_platform": {"webapp"}, "aid": {"6383"}, "channel": {"channel_pc_web"}, "pc_client_type": {"1"}, "version_code": {"290100"}, "version_name": {"29.1.0"}, "cookie_enabled": {"true"}, "screen_width": {"1920"}, "screen_height": {"1080"}, "browser_language": {"zh-CN"}, "browser_platform": {"Win32"}, "browser_name": {"Chrome"}, "browser_version": {"146.0.0.0"}, "browser_online": {"true"}, "engine_name": {"Blink"}, "engine_version": {"146.0.0.0"}, "os_name": {"Windows"}, "os_version": {"10"}, "cpu_core_num": {"12"}, "device_memory": {"8"}, "platform": {"PC"}, "downlink": {"10"}, "effective_type": {"4g"}, "round_trip_time": {"0"}, "update_version_code": {"170400"},
	}
	cookies := cookieValues(cookie)
	token := cookies["msToken"]
	if token == "" {
		// Match NativeSigner(fill_ms_token=True): a local protocol placeholder,
		// never a login credential or a replacement for UIFID/session cookies.
		const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+-"
		noise := make([]byte, 126)
		if _, err := rand.Read(noise); err != nil {
			panic(err)
		}
		for i := range noise {
			noise[i] = alphabet[int(noise[i])&63]
		}
		token = string(noise) + "=="
	}
	params.Set("msToken", token)
	// Python's dict/urlencode preserve insertion order. Sign and send that
	// exact order, including the token after the endpoint's aweme_id.
	keys := []string{"device_platform", "aid", "channel", "pc_client_type", "version_code", "version_name", "cookie_enabled", "screen_width", "screen_height", "browser_language", "browser_platform", "browser_name", "browser_version", "browser_online", "engine_name", "engine_version", "os_name", "os_version", "cpu_core_num", "device_memory", "platform", "downlink", "effective_type", "round_trip_time", "update_version_code"}
	for _, p := range extra {
		params.Set(p.key, p.value)
		keys = append(keys, p.key)
	}
	keys = append(keys, "msToken")
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(params.Get(key)))
	}
	query := strings.Join(parts, "&")
	query += "&a_bogus=" + url.QueryEscape(aBogus(query, desktopUA, nowMs))
	var uifid string
	for _, name := range []string{"uifid", "uifid_temp", "uifidtemp", "UIFID", "UIFID_TEMP", "UIFIDTEMP"} {
		if cookies[name] != "" {
			uifid = cookies[name]
			break
		}
	}
	headers := map[string]string{}
	if uifid != "" {
		if fp := cookies["s_v_web_id"]; fp != "" {
			query += "&verifyFp=" + url.QueryEscape(fp) + "&fp=" + url.QueryEscape(fp)
		}
		stamp := strconv.FormatInt(nowMs/1000, 10)
		query += "&uifid=" + url.QueryEscape(uifid) + "&timestamp=" + stamp
		// Keep pair order while reproducing the reference URLSearchParams escaping.
		query = strings.ReplaceAll(query, "+", "%20")
		query = strings.ReplaceAll(query, "%2A", "*")
		hash := md5.Sum([]byte(uifid + "_" + stamp + "_A96D855A08C0A9707F8BEF0D9A527E4E_" + query))
		signature := hex.EncodeToString(hash[:])
		query += "&x-secsdk-web-signature=" + signature
		headers["uifid"] = uifid
		headers["x-secsdk-web-signature"] = signature
		headers["x-secsdk-web-expire"] = stamp
	}
	return "https://www.douyin.com" + endpoint + "?" + query, headers
}
