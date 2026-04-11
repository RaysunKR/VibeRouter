package service

import (
	"time"
	"viberouter/internal/model"
)

// CallLogService handles call logging (file-based, no database)
type CallLogService struct{}

func NewCallLogService() *CallLogService {
	return &CallLogService{}
}

func (s *CallLogService) Log(log *model.CallLog) error {
	log.CreatedAt = time.Now()

	// Write to file (async, non-blocking)
	fileLogger := GetFileLogger()
	if fileLogger != nil {
		fileLogger.Log(&FileLogEntry{
			Username:    log.AdminUsername,
			ClientIP:   log.ClientIP,
			Provider:    string(log.Provider),
			ModelName:  log.ModelName,
			DisplayName: log.ModelDisplayName,
			ApiStyle:   log.ApiStyle,
			RequestPath: log.RequestPath,
			Method:     log.RequestMethod,
			StatusCode: log.StatusCode,
			ErrorMessage: log.ErrorMessage,
			LatencyMs:  log.LatencyMs,
		})
	}

	return nil
}
