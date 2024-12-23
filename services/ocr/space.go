package ocr

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	ocr "github.com/ranghetto/go_ocr_space"
	"google.golang.org/api/iterator"
)

// timestamp
type APIKey struct {
	Key        string    `firestore:"key"`
	UsageCount int       `firestore:"usage_count"`
	CreatedAt  time.Time `firestore:"created_at"`
	ExpiresAt  time.Time `firestore:"expires_at"`
}

// Get Firestore client
func getFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	projectID := os.Getenv("GCP_PROJECT_ID")
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create Firestore client: %v", err)
	}
	return client, nil
}

func getLeastUsedAPIKey(ctx context.Context, client *firestore.Client) (*APIKey, error) {
	// Define the usage limit

	query := client.Collection("OcrSpaceKey").
		Where("usage_count", ">", 0).
		Where("expires_at", ">", time.Now()).
		OrderBy("created_at", firestore.Asc).
		OrderBy("usage_count", firestore.Asc).
		Limit(1)

	iter := query.Documents(ctx)
	defer iter.Stop()

	doc, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, fmt.Errorf("no available API keys with usage count > 0")
		}
		return nil, fmt.Errorf("failed to fetch API key: %v", err)
	}

	var apiKey APIKey
	if err := doc.DataTo(&apiKey); err != nil {
		return nil, fmt.Errorf("failed to parse API key data: %v", err)
	}

	return &apiKey, nil
}

func decrementUsageCount(ctx context.Context, client *firestore.Client, key string) error {
	docRef := client.Collection("OcrSpaceKey").Doc(key)

	err := client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// Read the current usage count
		docSnap, err := tx.Get(docRef)
		if err != nil {
			return fmt.Errorf("failed to fetch document for key %s: %v", key, err)
		}

		// Ensure the usage_count field exists
		var apiKey APIKey
		if err := docSnap.DataTo(&apiKey); err != nil {
			return fmt.Errorf("failed to parse API key data: %v", err)
		}

		// Increment the usage count
		newUsageCount := apiKey.UsageCount - 1
		return tx.Update(docRef, []firestore.Update{
			{Path: "usage_count", Value: newUsageCount},
		})
	})

	if err != nil {
		return fmt.Errorf("failed to increment usage count in transaction: %v", err)
	}
	return nil
}

// Perform OCR using the least used API key
func SpaceOCRText(base64image string) (string, error) {
	ctx := context.Background()
	client, err := getFirestoreClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()

	// Fetch the least used API key
	apiKey, err := getLeastUsedAPIKey(ctx, client)
	if err != nil {
		return "", err
	}
	const prefix = "data:image/jpeg;base64,"
	if !strings.HasPrefix(base64image, prefix) {
		base64image = prefix + base64image
	}

	// Use the selected API key for OCR
	config := ocr.InitConfig(apiKey.Key, "eng", ocr.OCREngine2)
	result, err := config.ParseFromBase64(base64image)
	if err != nil {
		return "", err
	}

	// Increment usage count in Firestore
	if err := decrementUsageCount(ctx, client, apiKey.Key); err != nil {
		log.Printf("Failed to decrement usage count for key %s: %v\n", apiKey.Key, err)
	}

	return result.JustText(), nil
}

//https://myapi.ocr.space/conversions post to check apikey count. more details visit https://ocr.space/ocrapi/myapi
