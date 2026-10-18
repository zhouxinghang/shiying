package app

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestNativeSigningMatchesReference(t *testing.T) {
	if got := hex.EncodeToString(sm3([]byte("abc"))); got != "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0" {
		t.Fatal("SM3 published vector", got)
	}
	if got := hex.EncodeToString(sdkCipher([]byte{0, 1, 14}, []byte("shiying-native-test"))); got != "c3c2c2067ee09c500a4e0c4ae4f03814848890" {
		t.Fatal("reference SDK cipher", got)
	}
	// Oracle: ABogus._fields from src/dtk/signing/native/abogus.py, fixed time and input.
	f := bogusFields("aid=6383&aweme_id=7372484719365098803", desktopUA, 1789010742123)
	var ordered []byte
	for _, index := range fieldOrder {
		ordered = append(ordered, f[index])
	}
	if got := hex.EncodeToString(ordered); got != "010e1e8b006b000001d1210331180374a0008bef372c5900d40059890618000062520000a061b100010000296a892c000400" {
		t.Fatal("reference field layout", got)
	}
	// This output can be decoded independently with the repository's Python decoder.
	signature := aBogus("aid=6383&aweme_id=7372484719365098803", desktopUA, 1789010742123)
	t.Log("oracle-signature:", signature)
	if _, err := s4.DecodeString(signature); err != nil {
		t.Fatal(err)
	}
}

func TestDetailSigningKeepsVisitorAndQueryConsistent(t *testing.T) {
	raw, headers := signedDetailURL(fixtureID, "UIFID_TEMP=fixture+visitor; s_v_web_id=verify_fixture; msToken=fixture-token", 1789010742123)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("msToken") != "fixture-token" || q.Get("uifid") != "fixture+visitor" || q.Get("verifyFp") != "verify_fixture" || q.Get("fp") != "verify_fixture" {
		t.Fatal("visitor mismatch")
	}
	if len(q["a_bogus"]) != 1 || len(q["timestamp"]) != 1 || len(q["uifid"]) != 1 || headers["uifid"] != q.Get("uifid") {
		t.Fatal("duplicate or inconsistent parameters")
	}
	covered := strings.Split(u.RawQuery, "&x-secsdk-web-signature=")[0]
	sum := md5.Sum([]byte("fixture+visitor_1789010742_A96D855A08C0A9707F8BEF0D9A527E4E_" + covered))
	if q.Get("x-secsdk-web-signature") != hex.EncodeToString(sum[:]) || headers["x-secsdk-web-signature"] != q.Get("x-secsdk-web-signature") {
		t.Fatal("web signature mismatch")
	}
	_, emptyHeaders := signedDetailURL(fixtureID, "", 1789010742123)
	if len(emptyHeaders) != 0 {
		t.Fatal("must not invent a visitor identity")
	}
}

func TestConfiguredCookieUsesSignedAPIFirst(t *testing.T) {
	s := newStore(t)
	if err := s.SetCookie("UIFID_TEMP=synthetic-visitor; msToken=synthetic-token"); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(s)
	count := 0
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		count++
		if strings.Contains(req.URL.Path, "/aweme/v1/") {
			if req.Header.Get("User-Agent") != desktopUA || len(req.Header.Values("User-Agent")) != 1 || req.URL.Query().Get("a_bogus") == "" || req.Header.Get("x-secsdk-web-signature") == "" || req.Header.Get("Cookie") == "" {
				t.Error("missing or inconsistent signed identity")
			}
			return response(req, "application/json", fixture(t, "video_normal")), nil
		}
		return response(req, "text/html", []byte(`<script>window._ROUTER_DATA={"loaderData":{"video_(id)/page":{"itemId":"`+fixtureID+`"}}}</script>`)), nil
	})
	v, err := r.Resolve(context.Background(), "https://www.douyin.com/video/"+fixtureID)
	if err != nil || v.ID != fixtureID || count != 1 {
		t.Fatalf("fallback: %+v, requests=%d, err=%v", v, count, err)
	}
}
