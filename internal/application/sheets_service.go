package application

import (
	"fmt"
	"valhalla/pkg/sheets"
)

const (
	defaultSheetTitle = "Valhalla Stats"
	defaultClearRange = "A1:Z1000"
	defaultStartCell  = "A1"
)

type SheetsService interface {
	EnsureSheetExists() (string, error)
	UpdateStats(data [][]interface{}) error
	GetSpreadsheetURL() string
}

type SheetsServiceImpl struct {
	client         sheets.Client
	ownerEmail     string
	spreadsheetID  string
	spreadsheetURL string
}

func NewSheetsServiceImpl(client sheets.Client, ownerEmail string) *SheetsServiceImpl {
	return &SheetsServiceImpl{
		client:     client,
		ownerEmail: ownerEmail,
	}
}

func (s *SheetsServiceImpl) EnsureSheetExists() (string, error) {
	if s.spreadsheetID != "" {
		return s.spreadsheetURL, nil
	}

	id, url, err := s.client.CreateSpreadsheet(defaultSheetTitle)
	if err != nil {
		return "", fmt.Errorf("failed to create spreadsheet: %w", err)
	}
	s.spreadsheetID = id
	s.spreadsheetURL = url

	if s.ownerEmail != "" {
		if err := s.client.AddPermission(id, s.ownerEmail, "writer"); err != nil {
			return "", fmt.Errorf("failed to add owner permission: %w", err)
		}
	}

	if err := s.client.MakePublic(id); err != nil {
		return "", fmt.Errorf("failed to make spreadsheet public: %w", err)
	}

	return url, nil
}

func (s *SheetsServiceImpl) UpdateStats(data [][]interface{}) error {
	if s.spreadsheetID == "" {
		return fmt.Errorf("spreadsheet not initialized, call EnsureSheetExists first")
	}

	if err := s.client.ClearRange(s.spreadsheetID, defaultClearRange); err != nil {
		return fmt.Errorf("failed to clear spreadsheet: %w", err)
	}

	if err := s.client.UpdateValues(s.spreadsheetID, defaultStartCell, data); err != nil {
		return fmt.Errorf("failed to update spreadsheet: %w", err)
	}

	return nil
}

func (s *SheetsServiceImpl) GetSpreadsheetURL() string {
	if s.spreadsheetID == "" {
		return ""
	}
	return fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", s.spreadsheetID)
}

func (s *SheetsServiceImpl) SetSpreadsheetID(id string) {
	s.spreadsheetID = id
	s.spreadsheetURL = fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", id)
}
