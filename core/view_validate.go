package core

import (
	"fmt"
	"strings"
)

// validateViewCollections verifies that every view collection still resolves
// against the database and reports all broken ones at once.
//
// View queries are user authored SQL, so a query that is no longer valid would
// otherwise only surface later as an opaque runtime API error.
func (app *BaseApp) validateViewCollections() error {
	collections, err := app.FindAllCollections(CollectionTypeView)
	if err != nil {
		return fmt.Errorf("failed to load the view collections: %w", err)
	}

	var problems []string

	for _, collection := range collections {
		// a missing view means the stored query never made it into the database
		if !app.HasTable(collection.Name) {
			problems = append(problems, fmt.Sprintf("%s: the view does not exist in the database", collection.Name))
			continue
		}

		if _, err := app.TableInfo(collection.Name); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", collection.Name, err))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf(
			"invalid view collection(s) detected:\n  - %s\n\nView queries are stored SQL and must be valid for the current database. "+
				"Update or delete the listed view collections to continue.",
			strings.Join(problems, "\n  - "),
		)
	}

	return nil
}
