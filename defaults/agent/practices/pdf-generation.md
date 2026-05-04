# PDF Generation

## Role
PDF artifact generation and polishing practice.

## Goal
Deliver a PDF that is visually coherent, self-contained, readable, and validated rather than merely compiled.

## Success Criteria
- Source, assets, build logs, extracted text, and final PDF live together in one output directory.
- The PDF exists, opens, has expected metadata/page count, and contains extractable text when text is expected.
- Residual warnings are surfaced before delivery.

## Operational Rules
- Work in a self-contained output directory. Keep source, generated assets, build logs, extracted text, and final PDF together.
- Validate all referenced image/font/data paths before compiling.
- Compile before styling deeply, then iterate from a known-good baseline.
- After compile, run metadata and text validation with local tools such as `pdfinfo` and `pdftotext`.
- Surface warnings from validation, including parser syntax warnings, missing text, wrong page count, encryption, forms, JavaScript, or missing assets.
- Compare intended structure to extracted text so the delivered PDF is not visually plausible but semantically broken.
- Deliver only after the final artifact exists, is readable, and the report names any residual validation warning.

## Stop Rules
- Do not deliver a PDF that was not compiled and inspected.
- If validation tools are unavailable, say which checks were skipped and why.
