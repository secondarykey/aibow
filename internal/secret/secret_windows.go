//go:build windows

package secret

// secret_windows.go: Windows の DPAPI で包む・戻す。
//
// crypt32.dll の CryptProtectData / CryptUnprotectData を、標準ライブラリの syscall で呼ぶ(外部パッケージなし・CGO なし)。
// どちらも DATA_BLOB(長さとバイト列の先頭)を受け取り、結果を Windows が確保した DATA_BLOB に入れて返すので、
// 写し終えたら LocalFree で返す。
// CRYPTPROTECT_UI_FORBIDDEN を付けて、Windows が確認の窓を出さないようにする(アプリの裏で黙って呼ぶため)。

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Supported は、この OS で包めるか。
const Supported = true

// Kind は、この OS で包んだときの形。
const Kind = KindDPAPI

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

// cryptProtectUIForbidden は CRYPTPROTECT_UI_FORBIDDEN(確認の窓を出さない)。
const cryptProtectUIForbidden = 0x1

// dataBlob は Windows の DATA_BLOB。
type dataBlob struct {
	size uint32
	data *byte
}

func newBlob(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{size: uint32(len(b)), data: &b[0]}
}

// bytes は、Windows が返した DATA_BLOB の中身を Go のバイト列に写し、Windows の領域を返す。
func (b *dataBlob) bytes() []byte {
	defer procLocalFree.Call(uintptr(unsafe.Pointer(b.data)))
	return append([]byte(nil), unsafe.Slice(b.data, b.size)...)
}

// Protect は data を、今の Windows の利用者だけが戻せる形に包む。
func Protect(data []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(newBlob(data))),
		0, // 説明(使わない)
		0, // 追加のエントロピー(使わない。同じ利用者なら戻せればよい)
		0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("Windows の DPAPI で包めません: %w", err)
	}
	return out.bytes(), nil
}

// Unprotect は Protect で包んだものを戻す。ほかの PC・ほかの Windows の利用者が包んだものは戻せない。
func Unprotect(data []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(newBlob(data))),
		0, 0, 0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("Windows の DPAPI で戻せません(ほかの PC か、ほかの Windows の利用者が保存したものです): %w", err)
	}
	return out.bytes(), nil
}
