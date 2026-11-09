package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/media"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

const (
	maxSpeechInputBytes = 16 * 1024
	maxMediaModelBytes  = 256
	maxVoiceBytes       = 128
	maxSpeechCharacters = 16384
	ttsModelKey         = "mock-tts"
	ttsVoice            = "alloy"
	ttsProfileVersion   = "22222222-2222-4222-8222-222222222222"
)

type mediaReplay struct {
	digest       [32]byte
	unknown      bool
	status       int
	errorCode    string
	errorMessage string
	contentType  string
	body         []byte
}

type speechRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
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
	if !speechJSONContentType(c) {
		s.fail(c, http.StatusUnsupportedMediaType, "invalid_request", "语音请求必须使用 application/json")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, int64(media.MaxInputJSONBytes)+1))
	if err != nil || len(raw) > media.MaxInputJSONBytes || !decodeCanonicalJSON(bytes.NewReader(raw), &in, media.MaxInputJSONBytes, "model", "input", "voice", "response_format") || !validSpeechRequest(p, in) {
		s.fail(c, http.StatusBadRequest, "invalid_request", "语音合成参数无效")
		return
	}
	digest := sha256.Sum256(raw)
	key := c.GetHeader("Idempotency-Key")
	if len(c.Request.Header.Values("Idempotency-Key")) != 1 || key == "" || len(key) > 200 || strings.TrimSpace(key) != key {
		s.fail(c, http.StatusBadRequest, "invalid_request", "需要有效 Idempotency-Key")
		return
	}
	unlock := s.lockSpeechIdempotency(mediaReplayKey(p, "tts", key))
	defer unlock()
	replay, key, ok := s.mediaIdempotency(c, "tts", digest)
	if !ok {
		return
	}
	if replay != nil {
		if replay.errorCode != "" {
			s.fail(c, replay.status, replay.errorCode, replay.errorMessage)
			return
		}
		if replay.unknown {
			s.fail(c, http.StatusConflict, "provider_consumption_unknown", "供应商提交结果未知，禁止自动重试")
			return
		}
		c.Data(replay.status, replay.contentType, replay.body)
		return
	}
	if stored, found, err := s.loadMediaArtifactByIdempotency(c.Request.Context(), p, "tts", key, digest); err != nil {
		s.fail(c, http.StatusServiceUnavailable, "result_unavailable", "语音结果暂不可用")
		return
	} else if found {
		c.Data(http.StatusOK, stored.MediaType, stored.Body)
		return
	}
	requestID := c.GetString("request_id")
	if _, err := uuid.Parse(requestID); err != nil {
		requestID = uuid.NewString()
	}
	billingRefs, err := s.reserveMediaForRequest(c.Request.Context(), p.TenantID, p.UserID, p.KeyID, requestID, key, "tts", ttsModelKey, ttsProfileVersion, digest, int64(len([]rune(in.Input))))
	if err != nil {
		s.error(c, err)
		return
	}
	if billingRefs.attemptID == "" {
		billingRefs.attemptID = uuid.NewString()
	}
	if billingRefs.enabled && billingRefs.existing && billingRefs.state != "prepared" {
		s.fail(c, http.StatusConflict, "provider_consumption_unknown", "语音请求已有未决供应商消费，禁止重发")
		return
	}
	if billingRefs.enabled {
		if err = s.Billing.MarkMediaAttemptSending(c.Request.Context(), p.TenantID, billingRefs.attemptID); err != nil {
			if finalizeErr := s.finalizeMediaRequest(c.Request.Context(), billingRefs, "failed", "missing", 0); finalizeErr != nil {
				s.fail(c, http.StatusServiceUnavailable, "settlement_pending", "语音账务收尾暂不可用")
				return
			}
			s.error(c, err)
			return
		}
	}
	adapter := s.MediaAdapter
	if adapter == nil {
		s.fail(c, http.StatusServiceUnavailable, "unavailable", "语音服务未就绪")
		return
	}
	result, err := adapter.SynthesizeSpeech(c.Request.Context(), provider.SpeechRequest{RequestID: requestID, IdempotencyKey: key, Model: in.Model, Input: in.Input, Voice: in.Voice, ResponseFormat: in.ResponseFormat})
	if err != nil || !result.Sent {
		unknown := result.Sent
		if providerErr, ok := err.(*provider.MediaError); ok {
			unknown = unknown || providerErr.Sent || providerErr.Unknown
		}
		if unknown {
			if finalizeErr := s.finalizeMediaRequest(c.Request.Context(), billingRefs, "unknown", "unknown", 0); finalizeErr != nil {
				s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, errorCode: "settlement_pending", errorMessage: "语音账务收尾暂不可用", status: http.StatusServiceUnavailable})
				s.fail(c, http.StatusServiceUnavailable, "settlement_pending", "语音账务收尾暂不可用")
				return
			}
			s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, unknown: true, status: http.StatusConflict})
			s.fail(c, http.StatusConflict, "provider_consumption_unknown", "供应商提交结果未知，禁止自动重试")
			return
		}
		if finalizeErr := s.finalizeMediaRequest(c.Request.Context(), billingRefs, "failed", "missing", 0); finalizeErr != nil {
			s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, errorCode: "settlement_pending", errorMessage: "语音账务收尾暂不可用", status: http.StatusServiceUnavailable})
			s.fail(c, http.StatusServiceUnavailable, "settlement_pending", "语音账务收尾暂不可用")
			return
		}
		s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, errorCode: "provider_error", errorMessage: "语音供应商调用失败", status: http.StatusBadGateway})
		s.fail(c, http.StatusBadGateway, "provider_error", "语音供应商调用失败")
		return
	}
	quantity := int64(len([]rune(in.Input)))
	if result.Format != "wav" || result.MediaType != "audio/wav" || len(result.Data) == 0 || len(result.Data) > media.MaxDecodedBytes || validateSpeechWAV(result.Data) != nil || result.Usage.Status != provider.SpeechUsageConfirmedFinal || result.Usage.CharacterCount != quantity || result.Usage.CharacterCount <= 0 || result.Usage.CharacterCount > maxSpeechCharacters {
		if finalizeErr := s.finalizeMediaRequest(c.Request.Context(), billingRefs, "unknown", "unknown", 0); finalizeErr != nil {
			s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, errorCode: "settlement_pending", errorMessage: "语音账务收尾暂不可用", status: http.StatusServiceUnavailable})
			s.fail(c, http.StatusServiceUnavailable, "settlement_pending", "语音账务收尾暂不可用")
			return
		}
		s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, unknown: true, status: http.StatusConflict})
		s.fail(c, http.StatusBadGateway, "provider_protocol_error", "语音供应商结果无效")
		return
	}
	if err = s.finalizeMediaRequest(c.Request.Context(), billingRefs, "completed", provider.SpeechUsageConfirmedFinal, result.Usage.CharacterCount); err != nil {
		s.fail(c, http.StatusServiceUnavailable, "settlement_pending", "语音账务收尾暂不可用")
		return
	}
	if err = s.storeMediaArtifact(c.Request.Context(), billingRefs, "tts", result.MediaType, result.Data); err != nil {
		s.fail(c, http.StatusServiceUnavailable, "result_unavailable", "语音结果暂不可用")
		return
	}
	s.storeMediaReplay(p, "tts", key, mediaReplay{digest: digest, status: http.StatusOK, contentType: result.MediaType, body: result.Data})
	c.Data(http.StatusOK, result.MediaType, result.Data)
}

func speechJSONContentType(c *gin.Context) bool {
	values := c.Request.Header.Values("Content-Type")
	return len(values) == 1 && values[0] == "application/json"
}

func validSpeechRequest(p identity.Principal, in speechRequest) bool {
	quantity := len([]rune(in.Input))
	return in.Model == ttsModelKey && validMediaModel(p, in.Model) && utf8.ValidString(in.Input) && len(in.Input) > 0 && len(in.Input) <= maxSpeechInputBytes && quantity > 0 && quantity <= maxSpeechCharacters && in.Voice == ttsVoice && len(in.Voice) <= maxVoiceBytes && in.ResponseFormat == "wav"
}

func (s *Server) lockSpeechIdempotency(key string) func() {
	lock := &sync.Mutex{}
	value, _ := s.SpeechIdempotencyLocks.LoadOrStore(key, lock)
	lock = value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
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
			return provider.SpeechResponse{Sent: true, MediaType: "audio/wav", Format: "wav", Data: mockWAV(len([]rune(in.Input))), Usage: provider.SpeechUsage{Status: provider.SpeechUsageConfirmedFinal, CharacterCount: int64(len([]rune(in.Input)))}}, nil
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
