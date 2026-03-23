package main

import (
	"context"
	"log"
	"os"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var fsClient *firestore.Client

func initFirestore() {
	ctx := context.Background()
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = "easyai-gateway"
	}
	var err error
	fsClient, err = firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create Firestore client: %v", err)
	}
	log.Println("Firestore client initialized.")
}

func firestoreCollection() *firestore.CollectionRef {
	return fsClient.Collection("credits")
}

func loadFromFirestore(store map[string]*UserCredits) error {
	ctx := context.Background()
	docs, err := firestoreCollection().Documents(ctx).GetAll()
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var u UserCredits
		if err := doc.DataTo(&u); err != nil {
			log.Printf("Error parsing doc %s: %v", doc.Ref.ID, err)
			continue
		}
		store[u.LicenseID] = &u
	}
	return nil
}

func saveUserToFirestore(u *UserCredits) error {
	ctx := context.Background()
	_, err := firestoreCollection().Doc(u.LicenseID).Set(ctx, u)
	return err
}

func deleteUserFromFirestore(licenseID string) error {
	ctx := context.Background()
	_, err := firestoreCollection().Doc(licenseID).Delete(ctx)
	return err
}

func getUserFromFirestore(licenseID string) (*UserCredits, error) {
	ctx := context.Background()
	doc, err := firestoreCollection().Doc(licenseID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}
	var u UserCredits
	if err := doc.DataTo(&u); err != nil {
		return nil, err
	}
	return &u, nil
}
