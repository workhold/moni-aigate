package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/media"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

const (
	maxSpeechInputBytes = 16 * 1024
	maxMediaModelBytes  = 256
	maxVoiceBytes       = 128
)

type mediaReplay struct {
	digest      [32]byte
	unknown     bool
	status      int
	contentType string
	body        []byte
}

type speechRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
}

func readStrictMediaJSON(c *gin.Context, dst any, limit int) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return nil, media.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(dst); err != nil {
		return nil, media.ErrInvalidJSON
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, media.ErrInvalidJSON
	}
	return raw, nil
}

func mediaReplayKey(p identity.Principal, operation, idempotency string) string {
	return p.TenantID + "\x00" + p.KeyID + "\x00" + operation + "\x00" + idempotency
}

func (s *Server) loadMediaReplay(p identity.Principal, operation, key string, digest [32]byte) (*mediaReplay, bool) {
	value, ok := s.MediaIdempotency.Load(mediaReplayKey(p, operation, key))
	if !ok {
		return nil, false
	}
	replay := value.(mediaReplay)
	if replay.digest != digest {
		return &mediaReplay{status: http.StatusConflict}, true
	}
	replay.body = append([]byte(nil), replay.body...)
	return &replay, true
}

func (s *Server) storeMediaReplay(p identity.Principal, operation, key string, replay mediaReplay) {
	replay.body = append([]byte(nil), replay.body...)
	s.MediaIdempotency.Store(mediaReplayKey(p, operation, key), replay)
}

func (s *Server) mediaIdempotency(c *gin.Context, operation string, digest [32]byte) (*mediaReplay, string, bool) {
	p := c.MustGet("principal").(identity.Principal)
	key := c.GetHeader("Idempotency-Key")
	if key == "" || len(key) > 200 || strings.TrimSpace(key) != key {
		s.fail(c, http.StatusBadRequest, "invalid_request", "需要有效 Idempotency-Key")
		return nil, "", false
	}
	if replay, ok := s.loadMediaReplay(p, operation, key, digest); ok {
		if replay.status == http.StatusConflict {
			s.fail(c, http.StatusConflict, "idempotency_conflict", "Idempotency-Key 已用于不同请求")
			return nil, "", false
		}
		return replay, key, true
	}
	return nil, key, true
}

func mediaScopeAllowed(p identity.Principal, model string) bool {
	for _, scope := range p.Scopes {
		if scope == "*" || scope == model {
			return true
		}
	}
	return len(p.Scopes) == 0
}

func validMediaModel(p identity.Principal, model string) bool {
	return model != "" && len(model) <= maxMediaModelBytes && mediaScopeAllowed(p, model)
}

func (s *Server) synthesizeSpeech(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	var in speechRequest
	raw, err := readStrictMediaJSON(c, &in, media.MaxInputJSONBytes)
	if err != nil || !validMediaModel(p, in.Model) || len(in.Input) == 0 || len(in.Input) > maxSpeechInputBytes || len(in.Voice) == 0 || len(in.Voice) > maxVoiceBytes || in.ResponseFormat != "wav" {
		s.fail(c, http.StatusBadRequest, "invalid_request", "语音合成参数无效")
		return
	}
	digest := sha256.Sum256(raw)
	replay, key, ok := s.mediaIdempotency(c, "tts", digest)
	if !ok {
		return
	}
	if replay != nil {
		c.Data(replay.status, replay.contentType, replay.body)
		return
	}
	adapter := s.MediaAdapter
	if adapter == nil {
		s.fail(c, http.StatusServiceUnavailable, "unavailable", "语音服务未就绪")
		return
	}
	result, err := adapter.SynthesizeSpeech(c.Request.Context(), provider.SpeechRequest{RequestID: c.GetString("request_id"), IdempotencyKey: key, Model: in.Model, Input: in.Input, Voice: in.Voice, ResponseFormat: in.ResponseFormat})
	if err != nil || !result.Sent {
		unknown := result.Sent
		if providerErr, ok := err.(*provider.MediaError); ok {
			unknown = unknown || providerErr.Sent || providerErr.Unknown
		}
		if unknown {
			s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, unknown: true, status: http.StatusConflict})
			s.fail(c, http.StatusConflict, "provider_consumption_unknown", "供应商提交结果未知，禁止自动重试")
			return
		}
		s.fail(c, http.StatusBadGateway, "provider_error", "语音供应商调用失败")
		return
	}
	if len(result.Data) == 0 || result.MediaType != "audio/wav" || len(result.Data) > media.MaxDecodedBytes {
		s.fail(c, http.StatusBadGateway, "provider_protocol_error", "语音供应商结果无效")
		return
	}
	s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, status: http.StatusOK, contentType: result.MediaType, body: result.Data})
	c.Data(http.StatusOK, result.MediaType, result.Data)
}

func newDefaultMediaAdapter() provider.SynchronousMediaAdapter {
	return &provider.MockMediaAdapter{
		GenerateImageFunc: func(ctx context.Context, in provider.ImageRequest) (provider.ImageResponse, error) {
			if err := ctx.Err(); err != nil {
				return provider.ImageResponse{}, err
			}
			parts := strings.Split(in.Size, "x")
			if len(parts) != 2 {
				return provider.ImageResponse{}, provider.NewMediaError("invalid_size", http.StatusBadGateway, false, false)
			}
			width, _ := strconv.Atoi(parts[0])
			height, _ := strconv.Atoi(parts[1])
			picture := image.NewRGBA(image.Rect(0, 0, width, height))
			seed := sha256.Sum256([]byte(in.Prompt))
			fill := color.RGBA{R: seed[0], G: seed[1], B: seed[2], A: 255}
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					picture.SetRGBA(x, y, fill)
				}
			}
			var encoded bytes.Buffer
			if err := png.Encode(&encoded, picture); err != nil {
				return provider.ImageResponse{}, err
			}
			return provider.ImageResponse{Sent: true, MediaType: "image/png", Width: width, Height: height, Data: encoded.Bytes()}, nil
		},
		SynthesizeSpeechFunc: func(ctx context.Context, in provider.SpeechRequest) (provider.SpeechResponse, error) {
			if err := ctx.Err(); err != nil {
				return provider.SpeechResponse{}, err
			}
			return provider.SpeechResponse{Sent: true, MediaType: "audio/wav", Format: "wav", Data: mockWAV(len([]rune(in.Input)))}, nil
		},
	}
}

func mockWAV(samples int) []byte {
	if samples < 1 {
		samples = 1
	}
	if samples > 8000 {
		samples = 8000
	}
	dataSize := uint32(samples * 2)
	result := make([]byte, 44+dataSize)
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], 36+dataSize)
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], 1)
	binary.LittleEndian.PutUint32(result[24:28], 8000)
	binary.LittleEndian.PutUint32(result[28:32], 16000)
	binary.LittleEndian.PutUint16(result[32:34], 2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], dataSize)
	return result
}
