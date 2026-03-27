package cli

import (
	"encoding/json"
	"fmt"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/spf13/cobra"
)

var costsCmd = &cobra.Command{
	Use:   "costs",
	Short: "Print usage summary from a review run",
	RunE: func(cmd *cobra.Command, args []string) error {
		data, _ := cmd.Flags().GetString("data")
		if data == "" {
			fmt.Println("No usage data available.")
			return nil
		}

		var report review.UsageReport
		if err := json.Unmarshal([]byte(data), &report); err != nil {
			return fmt.Errorf("parsing usage data: %w", err)
		}

		review.PrintUsageSummary(&report)
		return nil
	},
}

func init() {
	costsCmd.Flags().String("data", "", "Usage JSON data")
	reviewCmd.AddCommand(costsCmd)
}
