# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0
#
# Creates a GCP service account for jitsudod and grants it the permissions
# needed to manage IAM conditional role bindings on GCP projects.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

resource "google_service_account" "jitsudod" {
  project      = var.project_id
  account_id   = var.service_account_id
  display_name = var.display_name
}

# roles/resourcemanager.projectIamAdmin covers both getIamPolicy and setIamPolicy,
# which are the only Cloud Resource Manager permissions jitsudod requires.
resource "google_project_iam_member" "jitsudod_iam_admin" {
  project = var.project_id
  role    = "roles/resourcemanager.projectIamAdmin"
  member  = "serviceAccount:${google_service_account.jitsudod.email}"
}
