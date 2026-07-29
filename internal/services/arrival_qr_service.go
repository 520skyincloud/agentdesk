package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/pkg/config"

	"github.com/liyue201/goqr"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	arrivalQRMaxDownloadBytes = 2 << 20
	arrivalQRCanvasSize       = 768
)

var ArrivalQRCodeService = newArrivalQRCodeService()

type arrivalQRCodeService struct {
	httpClient *http.Client
}

type arrivalQRCodeArtifact struct {
	OriginalPNGBase64  string
	PublishedPNGBase64 string
	PayloadHash        string
	ArtworkVerified    bool
}

func newArrivalQRCodeService() *arrivalQRCodeService {
	service := &arrivalQRCodeService{}
	service.httpClient = &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("二维码重定向次数过多")
			}
			return service.validateRemoteURL(req.URL)
		},
	}
	return service
}

func (s *arrivalQRCodeService) BuildArtifact(qrCodeURL string) (*arrivalQRCodeArtifact, error) {
	remoteURL, err := url.Parse(strings.TrimSpace(qrCodeURL))
	if err != nil || s.validateRemoteURL(remoteURL) != nil {
		return nil, fmt.Errorf("企业微信二维码地址不受信任")
	}
	req, err := http.NewRequest(http.MethodGet, remoteURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建二维码下载请求失败")
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("企业微信二维码下载失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("企业微信二维码下载失败")
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType != "" && !strings.HasPrefix(contentType, "image/") {
		return nil, fmt.Errorf("企业微信二维码资源类型无效")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, arrivalQRMaxDownloadBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > arrivalQRMaxDownloadBytes {
		return nil, fmt.Errorf("企业微信二维码资源大小无效")
	}
	return buildArrivalQRCodeArtifact(raw)
}

func buildArrivalQRCodeArtifact(source []byte) (*arrivalQRCodeArtifact, error) {
	sourceImage, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("企业微信二维码图片无法解析")
	}
	payload, err := decodeSingleQRCodePayload(sourceImage)
	if err != nil {
		return nil, fmt.Errorf("企业微信官方二维码无法解码")
	}
	sum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(sum[:])

	sourcePNG, err := normalizeArrivalPNG(sourceImage)
	if err != nil {
		return nil, fmt.Errorf("标准化企业微信二维码失败")
	}
	artifact := &arrivalQRCodeArtifact{
		OriginalPNGBase64:  base64.StdEncoding.EncodeToString(sourcePNG),
		PublishedPNGBase64: base64.StdEncoding.EncodeToString(sourcePNG),
		PayloadHash:        payloadHash,
		ArtworkVerified:    false,
	}
	artwork, err := renderArrivalFlowQRCode(string(payload), sourceImage)
	if err != nil {
		return artifact, nil
	}
	decodedArtwork, err := decodeSingleQRCodePayload(compositeArrivalQRForVerification(artwork))
	if err != nil || !bytes.Equal(decodedArtwork, payload) {
		return artifact, nil
	}
	var output bytes.Buffer
	if err := png.Encode(&output, artwork); err != nil {
		return artifact, nil
	}
	artifact.PublishedPNGBase64 = base64.StdEncoding.EncodeToString(output.Bytes())
	artifact.ArtworkVerified = true
	return artifact, nil
}

func decodeSingleQRCodePayload(img image.Image) ([]byte, error) {
	codes, err := goqr.Recognize(img)
	if err != nil || len(codes) != 1 || len(codes[0].Payload) == 0 {
		return nil, fmt.Errorf("二维码数量或内容无效")
	}
	return append([]byte(nil), codes[0].Payload...), nil
}

func normalizeArrivalPNG(img image.Image) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, arrivalQRCanvasSize, arrivalQRCanvasSize))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	scaleImageNearest(canvas, img, canvas.Bounds())
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func renderArrivalFlowQRCode(payload string, source image.Image) (*image.NRGBA, error) {
	code, err := qrcode.New(payload, qrcode.Highest)
	if err != nil {
		return nil, err
	}
	code.BackgroundColor = color.NRGBA{R: 255, G: 255, B: 255, A: 0}
	code.ForegroundColor = color.NRGBA{R: 22, G: 31, B: 45, A: 255}
	qrImage := code.Image(arrivalQRCanvasSize)
	canvas := image.NewNRGBA(image.Rect(0, 0, arrivalQRCanvasSize, arrivalQRCanvasSize))
	draw.Draw(canvas, canvas.Bounds(), qrImage, image.Point{}, draw.Src)

	// The official contact QR commonly carries a center avatar. Preserve that
	// visual identity inside a guarded white patch, then verify the result.
	sourceBounds := source.Bounds()
	sourceSide := min(sourceBounds.Dx(), sourceBounds.Dy())
	cropSide := max(sourceSide*16/100, 1)
	crop := image.Rect(
		sourceBounds.Min.X+(sourceBounds.Dx()-cropSide)/2,
		sourceBounds.Min.Y+(sourceBounds.Dy()-cropSide)/2,
		sourceBounds.Min.X+(sourceBounds.Dx()+cropSide)/2,
		sourceBounds.Min.Y+(sourceBounds.Dy()+cropSide)/2,
	)
	patchSide := arrivalQRCanvasSize * 18 / 100
	patch := image.Rect(
		(arrivalQRCanvasSize-patchSide)/2,
		(arrivalQRCanvasSize-patchSide)/2,
		(arrivalQRCanvasSize+patchSide)/2,
		(arrivalQRCanvasSize+patchSide)/2,
	)
	padding := arrivalQRCanvasSize / 90
	draw.Draw(canvas, patch.Inset(-padding), image.NewUniform(color.White), image.Point{}, draw.Src)
	scaleImageNearest(canvas.SubImage(patch).(*image.NRGBA), source, crop)
	return canvas, nil
}

func compositeArrivalQRForVerification(img image.Image) image.Image {
	canvas := image.NewNRGBA(img.Bounds())
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(canvas, canvas.Bounds(), img, img.Bounds().Min, draw.Over)
	return canvas
}

func scaleImageNearest(dst *image.NRGBA, src image.Image, srcRect image.Rectangle) {
	dstRect := dst.Bounds()
	if dstRect.Empty() || srcRect.Empty() {
		return
	}
	for y := dstRect.Min.Y; y < dstRect.Max.Y; y++ {
		sourceY := srcRect.Min.Y + (y-dstRect.Min.Y)*srcRect.Dy()/dstRect.Dy()
		for x := dstRect.Min.X; x < dstRect.Max.X; x++ {
			sourceX := srcRect.Min.X + (x-dstRect.Min.X)*srcRect.Dx()/dstRect.Dx()
			dst.Set(x, y, src.At(sourceX, sourceY))
		}
	}
}

func (s *arrivalQRCodeService) validateRemoteURL(remoteURL *url.URL) error {
	if remoteURL == nil || !strings.EqualFold(remoteURL.Scheme, "https") || remoteURL.User != nil {
		return fmt.Errorf("二维码地址必须为 HTTPS")
	}
	host := strings.ToLower(strings.TrimSpace(remoteURL.Hostname()))
	if host == "" || net.ParseIP(host) != nil || host == "localhost" {
		return fmt.Errorf("二维码地址主机无效")
	}
	suffixes := config.Current().Arrival.QRCodeAllowedHostSuffixes
	if len(suffixes) == 0 {
		suffixes = []string{"wework.qpic.cn", "wwcdn.weixin.qq.com", "work.weixin.qq.com"}
	}
	suffixes = slices.Clone(suffixes)
	for _, suffix := range suffixes {
		suffix = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
		if suffix != "" && (host == suffix || strings.HasSuffix(host, "."+suffix)) {
			return nil
		}
	}
	return fmt.Errorf("二维码地址主机不在白名单")
}
