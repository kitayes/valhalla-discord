package integration

import (
	"context"
	"fmt"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type SheetService struct {
	sheetsSr *sheets.Service
	driveSr  *drive.Service
	sheetID  string
}

func NewSheetService(credJSON string) (*SheetService, error) {
	ctx := context.Background()

	srv, err := sheets.NewService(ctx, option.WithCredentialsFile(credJSON))
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve Sheets client: %v", err)
	}

	drv, err := drive.NewService(ctx, option.WithCredentialsFile(credJSON))
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve Drive client: %v", err)
	}

	return &SheetService{
		sheetsSr: srv,
		driveSr:  drv,
	}, nil
}

func (s *SheetService) SetSpreadsheetID(id string) {
	s.sheetID = id
}

func (s *SheetService) EnsureSheetExists(title, ownerEmail string) (string, string, error) {
	if s.sheetID != "" {
		return s.sheetID, fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", s.sheetID), nil
	}

	resp, err := s.sheetsSr.Spreadsheets.Create(&sheets.Spreadsheet{
		Properties: &sheets.SpreadsheetProperties{
			Title: title,
		},
	}).Do()
	if err != nil {
		return "", "", err
	}
	s.sheetID = resp.SpreadsheetId
	url := resp.SpreadsheetUrl

	_, err = s.driveSr.Permissions.Create(s.sheetID, &drive.Permission{
		Type:         "user",
		Role:         "writer",
		EmailAddress: ownerEmail,
	}).Do()
	if err != nil {
		return "", "", fmt.Errorf("failed to add owner: %v", err)
	}

	_, err = s.driveSr.Permissions.Create(s.sheetID, &drive.Permission{
		Type: "anyone",
		Role: "reader",
	}).Do()
	if err != nil {
		return "", "", fmt.Errorf("failed to make public: %v", err)
	}

	return s.sheetID, url, nil
}

func (s *SheetService) UpdateStats(data [][]interface{}) error {
	if s.sheetID == "" {
		return fmt.Errorf("sheet not initialized")
	}

	_, err := s.sheetsSr.Spreadsheets.Values.Clear(s.sheetID, "A1:Z1000", &sheets.ClearValuesRequest{}).Do()
	if err != nil {
		return err
	}

	valRange := &sheets.ValueRange{
		Values: data,
	}
	_, err = s.sheetsSr.Spreadsheets.Values.Update(s.sheetID, "A1", valRange).ValueInputOption("RAW").Do()

	return err
}
