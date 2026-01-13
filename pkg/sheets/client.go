package sheets

import (
	"context"
	"fmt"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type Client interface {
	CreateSpreadsheet(title string) (spreadsheetID, url string, err error)
	AddPermission(spreadsheetID, email, role string) error
	MakePublic(spreadsheetID string) error
	ClearRange(spreadsheetID, rangeStr string) error
	UpdateValues(spreadsheetID, rangeStr string, values [][]interface{}) error
}

type GoogleSheetsClient struct {
	sheets *sheets.Service
	drive  *drive.Service
}

func NewGoogleSheetsClient(credentialsPath string) (*GoogleSheetsClient, error) {
	ctx := context.Background()

	sheetsSrv, err := sheets.NewService(ctx, option.WithCredentialsFile(credentialsPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create sheets service: %w", err)
	}

	driveSrv, err := drive.NewService(ctx, option.WithCredentialsFile(credentialsPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}

	return &GoogleSheetsClient{
		sheets: sheetsSrv,
		drive:  driveSrv,
	}, nil
}

func (c *GoogleSheetsClient) CreateSpreadsheet(title string) (string, string, error) {
	resp, err := c.sheets.Spreadsheets.Create(&sheets.Spreadsheet{
		Properties: &sheets.SpreadsheetProperties{
			Title: title,
		},
	}).Do()
	if err != nil {
		return "", "", fmt.Errorf("failed to create spreadsheet: %w", err)
	}
	return resp.SpreadsheetId, resp.SpreadsheetUrl, nil
}

func (c *GoogleSheetsClient) AddPermission(spreadsheetID, email, role string) error {
	_, err := c.drive.Permissions.Create(spreadsheetID, &drive.Permission{
		Type:         "user",
		Role:         role,
		EmailAddress: email,
	}).Do()
	if err != nil {
		return fmt.Errorf("failed to add permission: %w", err)
	}
	return nil
}

func (c *GoogleSheetsClient) MakePublic(spreadsheetID string) error {
	_, err := c.drive.Permissions.Create(spreadsheetID, &drive.Permission{
		Type: "anyone",
		Role: "reader",
	}).Do()
	if err != nil {
		return fmt.Errorf("failed to make spreadsheet public: %w", err)
	}
	return nil
}

func (c *GoogleSheetsClient) ClearRange(spreadsheetID, rangeStr string) error {
	_, err := c.sheets.Spreadsheets.Values.Clear(spreadsheetID, rangeStr, &sheets.ClearValuesRequest{}).Do()
	if err != nil {
		return fmt.Errorf("failed to clear range: %w", err)
	}
	return nil
}

func (c *GoogleSheetsClient) UpdateValues(spreadsheetID, rangeStr string, values [][]interface{}) error {
	valRange := &sheets.ValueRange{Values: values}
	_, err := c.sheets.Spreadsheets.Values.Update(spreadsheetID, rangeStr, valRange).ValueInputOption("RAW").Do()
	if err != nil {
		return fmt.Errorf("failed to update values: %w", err)
	}
	return nil
}
