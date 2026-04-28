# PDF Generation

Use this practice when creating or polishing a PDF artifact for the user.

Operational rules:
- Work in a self-contained output directory. Keep source, generated assets, build logs, extracted text, and final PDF together.
- Validate all referenced image/font/data paths before compiling.
- Compile before styling deeply, then iterate from a known-good baseline.
- After compile, run metadata and text validation with local tools such as `pdfinfo` and `pdftotext`.
- Surface warnings from validation, including parser syntax warnings, missing text, wrong page count, encryption, forms, JavaScript, or missing assets.
- Compare intended structure to extracted text so the delivered PDF is not visually plausible but semantically broken.
- Deliver only after the final artifact exists, is readable, and the report names any residual validation warning.
