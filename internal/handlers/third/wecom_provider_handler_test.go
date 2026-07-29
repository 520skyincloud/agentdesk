package third

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/tracex"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWeComProviderCallbackHTTPContract(t *testing.T) {
	fixture := setupWeComProviderHandlerTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/command-callback", WeComProviderCommandCallback)
	router.Any("/data-callback", WeComProviderDataCallback)

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	t.Run("valid command GET returns decrypted echo", func(t *testing.T) {
		nonce := "handler-command-get"
		echo := "official-command-echo"
		encrypted := encryptWeComHandlerCallback(t, fixture.encodingAESKey, "provider-enterprise-corp", []byte(echo))
		query := callbackQuery(
			weComHandlerSignature(fixture.callbackToken, timestamp, nonce, encrypted),
			timestamp,
			nonce,
			encrypted,
		)
		recorder := serveWeComProviderRequest(router, http.MethodGet, "/command-callback?"+query.Encode(), nil)
		if recorder.Code != http.StatusOK || recorder.Body.String() != echo {
			t.Fatalf("status=%d body=%q, want 200 and decrypted echo", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("invalid command GET returns 400", func(t *testing.T) {
		nonce := "handler-command-get-invalid"
		encrypted := encryptWeComHandlerCallback(t, fixture.encodingAESKey, "provider-enterprise-corp", []byte("echo"))
		query := callbackQuery("invalid-signature", timestamp, nonce, encrypted)
		recorder := serveWeComProviderRequest(router, http.MethodGet, "/command-callback?"+query.Encode(), nil)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%q, want 400", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("valid data GET remains accepted", func(t *testing.T) {
		nonce := "handler-data-get"
		echo := "official-data-echo"
		encrypted := encryptWeComHandlerCallback(t, fixture.encodingAESKey, "authorized-enterprise-corp", []byte(echo))
		query := callbackQuery(
			weComHandlerSignature(fixture.callbackToken, timestamp, nonce, encrypted),
			timestamp,
			nonce,
			encrypted,
		)
		recorder := serveWeComProviderRequest(router, http.MethodGet, "/data-callback?"+query.Encode(), nil)
		if recorder.Code != http.StatusOK || recorder.Body.String() != echo {
			t.Fatalf("status=%d body=%q, want 200 and decrypted echo", recorder.Code, recorder.Body.String())
		}
	})

	payload := []byte(fmt.Sprintf(
		"<xml><SuiteId><![CDATA[%s]]></SuiteId><InfoType><![CDATA[suite_ticket]]></InfoType><TimeStamp>%s</TimeStamp><SuiteTicket><![CDATA[%s]]></SuiteTicket></xml>",
		fixture.suiteID,
		timestamp,
		randomHexForHandlerTest(t, 32),
	))

	t.Run("valid command POST returns success", func(t *testing.T) {
		nonce := "handler-command-post"
		encrypted := encryptWeComHandlerCallback(t, fixture.encodingAESKey, fixture.suiteID, payload)
		body := []byte("<xml><Encrypt><![CDATA[" + encrypted + "]]></Encrypt></xml>")
		query := callbackQuery(
			weComHandlerSignature(fixture.callbackToken, timestamp, nonce, encrypted),
			timestamp,
			nonce,
			"",
		)
		recorder := serveWeComProviderRequest(router, http.MethodPost, "/command-callback?"+query.Encode(), body)
		if recorder.Code != http.StatusOK || recorder.Body.String() != "success" {
			t.Fatalf("status=%d body=%q, want 200 success", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("invalid command POST never returns fake success", func(t *testing.T) {
		nonce := "handler-command-post-invalid"
		encrypted := encryptWeComHandlerCallback(t, fixture.encodingAESKey, "provider-enterprise-corp", payload)
		body := []byte("<xml><Encrypt><![CDATA[" + encrypted + "]]></Encrypt></xml>")
		query := callbackQuery(
			weComHandlerSignature(fixture.callbackToken, timestamp, nonce, encrypted),
			timestamp,
			nonce,
			"",
		)
		recorder := serveWeComProviderRequest(router, http.MethodPost, "/command-callback?"+query.Encode(), body)
		if recorder.Code == http.StatusOK || recorder.Body.String() == "success" {
			t.Fatalf("status=%d body=%q, invalid POST returned fake success", recorder.Code, recorder.Body.String())
		}
	})
}

type weComProviderHandlerFixture struct {
	suiteID        string
	callbackToken  string
	encodingAESKey string
}

func setupWeComProviderHandlerTest(t *testing.T) weComProviderHandlerFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.WeComSuiteCredential{}, &models.WeComProviderCallbackEvent{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)

	callbackKey := randomBytesForHandlerTest(t, 32)
	dataKey := randomBytesForHandlerTest(t, 32)
	fixture := weComProviderHandlerFixture{
		suiteID:        "suite-handler-test",
		callbackToken:  randomHexForHandlerTest(t, 32),
		encodingAESKey: strings.TrimSuffix(base64.StdEncoding.EncodeToString(callbackKey), "="),
	}
	previous, hadPrevious := config.LookupCurrent()
	config.SetCurrent(&config.Config{Arrival: config.ArrivalConfig{
		DataMasterKey:               base64.StdEncoding.EncodeToString(dataKey),
		DataMasterKeyID:             "handler-test-key",
		IdentityHMACKey:             randomHexForHandlerTest(t, 32),
		SessionSecret:               randomHexForHandlerTest(t, 32),
		WeComSuiteID:                fixture.suiteID,
		WeComProviderCallbackToken:  fixture.callbackToken,
		WeComProviderEncodingAESKey: fixture.encodingAESKey,
	}})
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if hadPrevious {
			config.SetCurrent(&previous)
			return
		}
		config.SetCurrent(&config.Config{})
	})
	return fixture
}

func callbackQuery(signature, timestamp, nonce, echo string) url.Values {
	query := url.Values{
		"msg_signature": {signature},
		"timestamp":     {timestamp},
		"nonce":         {nonce},
	}
	if echo != "" {
		query.Set("echostr", echo)
	}
	return query
}

func serveWeComProviderRequest(router http.Handler, method, target string, body []byte) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set(tracex.RequestIDHeader, "wecom-handler-test-request")
	router.ServeHTTP(recorder, request)
	return recorder
}

func encryptWeComHandlerCallback(t *testing.T, encodingAESKey, receiveID string, message []byte) string {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodingAESKey) + "=")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := bytes.NewBuffer(randomBytesForHandlerTest(t, 16))
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(message)))
	plaintext.Write(length[:])
	plaintext.Write(message)
	plaintext.WriteString(receiveID)
	raw := plaintext.Bytes()
	padding := 32 - len(raw)%32
	raw = append(raw, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := make([]byte, len(raw))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(encrypted, raw)
	return base64.StdEncoding.EncodeToString(encrypted)
}

func weComHandlerSignature(token, timestamp, nonce, encrypted string) string {
	values := []string{token, timestamp, nonce, encrypted}
	sort.Strings(values)
	sum := sha1.Sum([]byte(strings.Join(values, "")))
	return hex.EncodeToString(sum[:])
}

func randomBytesForHandlerTest(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func randomHexForHandlerTest(t *testing.T, size int) string {
	t.Helper()
	return hex.EncodeToString(randomBytesForHandlerTest(t, size))
}
