package services

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
)

type weComEncryptedEnvelope struct {
	XMLName xml.Name `xml:"xml"`
	Encrypt string   `xml:"Encrypt"`
}

func verifyWeComCallbackSignature(token, timestamp, nonce, encrypted, signature string) bool {
	values := []string{
		strings.TrimSpace(token),
		strings.TrimSpace(timestamp),
		strings.TrimSpace(nonce),
		strings.TrimSpace(encrypted),
	}
	slices.Sort(values)
	sum := sha1.Sum([]byte(strings.Join(values, "")))
	return strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(signature))
}

func decryptWeComCallback(encodingAESKey, encrypted string, expectedReceiveIDs ...string) ([]byte, string, error) {
	key, err := decodeWeComCallbackAESKey(encodingAESKey)
	if err != nil {
		return nil, "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return nil, "", fmt.Errorf("企业微信回调密文不是有效 Base64")
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, "", fmt.Errorf("企业微信回调密文长度无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, "", fmt.Errorf("初始化企业微信回调解密器失败")
	}
	plaintext := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plaintext, raw)
	plaintext, err = unpadWeComCallback(plaintext)
	if err != nil {
		return nil, "", err
	}
	if len(plaintext) < 20 {
		return nil, "", fmt.Errorf("企业微信回调明文长度无效")
	}
	messageLength := int(binary.BigEndian.Uint32(plaintext[16:20]))
	if messageLength < 0 || 20+messageLength > len(plaintext) {
		return nil, "", fmt.Errorf("企业微信回调消息长度无效")
	}
	message := append([]byte(nil), plaintext[20:20+messageLength]...)
	receiveID := string(plaintext[20+messageLength:])
	if len(expectedReceiveIDs) > 0 {
		matched := false
		for _, expected := range expectedReceiveIDs {
			if strings.TrimSpace(expected) != "" && receiveID == strings.TrimSpace(expected) {
				matched = true
				break
			}
		}
		if !matched {
			return nil, "", fmt.Errorf("企业微信回调接收方不匹配")
		}
	}
	return message, receiveID, nil
}

func decodeWeComCallbackEnvelope(body []byte) (string, error) {
	envelope := weComEncryptedEnvelope{}
	if err := xml.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("企业微信回调 XML 无效")
	}
	if strings.TrimSpace(envelope.Encrypt) == "" {
		return "", fmt.Errorf("企业微信回调缺少 Encrypt")
	}
	return strings.TrimSpace(envelope.Encrypt), nil
}

func decodeWeComCallbackAESKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if len(value) != 43 {
		return nil, fmt.Errorf("企业微信回调 EncodingAESKey 无效")
	}
	key, err := base64.StdEncoding.DecodeString(value + "=")
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("企业微信回调 EncodingAESKey 无效")
	}
	return key, nil
}

func unpadWeComCallback(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, fmt.Errorf("企业微信回调填充无效")
	}
	padding := int(value[len(value)-1])
	if padding <= 0 || padding > 32 || padding > len(value) {
		return nil, fmt.Errorf("企业微信回调填充无效")
	}
	for _, b := range value[len(value)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("企业微信回调填充无效")
		}
	}
	return value[:len(value)-padding], nil
}

func encryptWeComCallbackForTest(encodingAESKey, receiveID string, message []byte) (string, error) {
	key, err := decodeWeComCallbackAESKey(encodingAESKey)
	if err != nil {
		return "", err
	}
	randomPrefix := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, randomPrefix); err != nil {
		return "", err
	}
	plaintext := bytes.NewBuffer(randomPrefix)
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
		return "", err
	}
	encrypted := make([]byte, len(raw))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(encrypted, raw)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}
