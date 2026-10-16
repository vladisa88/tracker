# Issue tracker: Local Markdown

- Feature directory: `.scratch/<feature-slug>/`.
- Specification: `spec.md` in the feature directory.
- Tickets: `issues/<NN>-<slug>.md`, numbered from 01
  in dependency order, one file per ticket.
- Each ticket declares its acceptance criteria,
  `Blocked by:` dependencies and `Status:`.
- Triage status names are defined in `triage-labels.md`.
- Append discussion under `## Comments`.

Publishing means creating a local Markdown file.
Fetching a ticket means reading its referenced file.
Start a ticket only after its blockers are completed.
Record completion and validation results in the ticket.
