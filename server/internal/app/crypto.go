package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
)

func secretKey(root, file string, create bool) ([]byte, error) {
	path := filepath.Join(root, file)
	key, e := os.ReadFile(path)
	if os.IsNotExist(e) && create {
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if os.IsExist(e) {
			return secretKey(root, file, false)
		}
		if e != nil {
			return nil, e
		}
		_, e = f.Write(key)
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
		return key, nil
	}
	if e != nil || len(key) != 32 {
		return nil, errors.New("加密密钥缺失或损坏，请恢复原密钥文件。")
	}
	return key, nil
}
func seal(root, file, plain, aad string) (string, error) {
	k, e := secretKey(root, file, true)
	if e != nil {
		return "", e
	}
	b, e := aes.NewCipher(k)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	iv := make([]byte, 12)
	if _, e = rand.Read(iv); e != nil {
		return "", e
	}
	sealed := g.Seal(nil, iv, []byte(plain), []byte(aad))
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	out := append(append(iv, tag...), ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}
func unseal(root, file, envelope, aad string) (string, error) {
	k, e := secretKey(root, file, false)
	if e != nil {
		return "", e
	}
	b, e := aes.NewCipher(k)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	data, e := base64.StdEncoding.DecodeString(envelope)
	if e != nil || len(data) < 29 {
		return "", errors.New("加密凭据格式损坏。")
	}
	ct := append(append([]byte{}, data[28:]...), data[12:28]...)
	plain, e := g.Open(nil, data[:12], ct, []byte(aad))
	if e != nil {
		return "", errors.New("无法解密凭据，请恢复原密钥或重新配置。")
	}
	return string(plain), nil
}
func decryptSecret(root, keyFile, envelope string) (string, error) {
	return unseal(root, keyFile, envelope, "starindex-commerce")
}
func encryptSecret(root, keyFile, plain string) (string, error) {
	return seal(root, keyFile, plain, "starindex-commerce")
}
