# LICENSE — SHEYTAN Music Visualizer licensing

Copyright © 2024–2026 Parsaetak (https://github.com/Parsaetak). All rights
reserved.

This is the **sole licensing artifact** of SHEYTAN Music Visualizer ("the
Software"). It carries the complete licensing picture in one document: the
licensing model, the full legal text of the license in force (the Parsaetak
Proprietary License v1.1), the trademark notice, the third-party attribution
inventory, and the governance/contact points. No other license, licence,
notice, or copying file in this repository carries legal authority.

---

## 1. Licensing model

SHEYTAN Music Visualizer is proprietary software. Every file in this
repository — and every artifact built from it — is governed by the **Parsaetak
Proprietary License v1.1** (§3 of this document):

| Material | Classification |
|---|---|
| First-party product code (`cmd/`, `internal/`, `ui/`) | Proprietary |
| Documentation (`readme.md`, `agent.md`, `roadmap.md`, `ai-notes.md`, this file) | Proprietary |
| Brand assets, designs and identity constants (including `internal/brand`) | Proprietary (trademarks of Parsaetak) |
| Build, release and CI machinery (`scripts/`, `.github/`) | Proprietary |
| Test suites (`*_test.go`, fixtures) | Proprietary (verification records of proprietary components) |
| Third-party dependencies (`go.mod` / `go.sum` graph) | Governed by their own licenses (§4) — never relicensed by this document |

The default is Proprietary. There are **no open-designated first-party
components** in Phase 1. If the licensor later designates a component as
open, that designation will be recorded here with an SPDX identifier and the
full open license text; until then, no open licensing applies to any
first-party material.

This document is adapted from the Parsaetak Proprietary License v1.1 as
published for the licensor's SHEYTAN-Local-Agent project
(https://github.com/SHEYTANx13/SHEYTAN-LA, `LICENSE.md` §5, retrieved
2026-11-04). The substantive legal terms — grant, intellectual property,
trademark, distribution restrictions, derivative-works restrictions,
privacy, acceptable use, warranty disclaimer, termination and changes — are
preserved unchanged; the scope statements and defined Software name are
adapted to SHEYTAN Music Visualizer. Unlike the reference repository, this
product has no mixed Apache-2.0 component and therefore no Apache-2.0 text
appears here.

---

## 2. IMPORTANT — READ CAREFULLY

By installing, copying, or otherwise using SHEYTAN Music Visualizer ("the
Software") you agree to be bound by the terms of the license in §3. If you
do not agree, do not install or use the Software.

---

## 3. Parsaetak Proprietary License v1.1

<!-- proprietary-text-begin -->

PARSAETAK PROPRIETARY LICENSE
=============================
Version 1.1 · Last updated: 2026-09-26

Copyright © 2024–2026 Parsaetak (https://github.com/Parsaetak). All rights
reserved.

SCOPE OF THIS LICENSE
---------------------
This license governs every component of SHEYTAN Music Visualizer. All
first-party components of the Software are Proprietary under this license:
the SHEYTAN-specific mechanisms, the product-specific implementation, the
assets and designs, the documentation, and all other material of the
Software. Third-party dependencies are governed by their own licenses and
are not relicensed by this document (see §4 of LICENSE.md).

IMPORTANT — READ CAREFULLY. By installing, copying, or otherwise using
SHEYTAN Music Visualizer ("the Software") you agree to be bound by the terms
of this agreement. If you do not agree, do not install or use the Software.

1. GRANT OF LICENSE
   Subject to the terms below, Parsaetak ("the Licensor") grants you a
   personal, non-exclusive, non-transferable, revocable license to install
   and run the Software on computers you own or control, for any lawful
   personal or commercial purpose.

2. INTELLECTUAL PROPERTY
   The Software — its source code, binaries, documentation, icons, and
   designs — is and remains the exclusive property of Parsaetak and its
   contributors. No ownership rights are transferred to you by this
   license. This license attaches to the concrete expressions of the
   Software (actual code, documentation, assets, and implementations); it
   does not assert that abstract ideas, techniques, algorithms, or
   functionality described by those expressions are themselves protected
   intellectual property.

3. TRADEMARK
   "SHEYTAN", "SHEYTAN Music Visualizer", and the SHEYTAN logo are
   trademarks of Parsaetak. You may not use the marks (or confusingly
   similar marks) to name or promote products, forks, or derivative works
   without prior written permission. Referring to the Software by its name
   for the purpose of description, review, or interoperability is
   permitted.

4. DISTRIBUTION
   You may NOT redistribute, sublicense, sell, rent, lease, or host the
   Software (in whole or in part, original or modified) without prior
   written permission. Sharing an unmodified official release archive for
   non-commercial personal use is permitted, provided all files —
   including this license — remain intact.

5. DERIVATIVE WORKS
   You may modify the Software for your own personal use. You may NOT
   distribute modified versions, rebranded builds, or extractions of the
   proprietary source code without prior written permission.

6. LOCAL-FIRST PRIVACY
   The Software is designed to run audio analysis, rendering and export
   locally on your machine, with all data stored inside your own
   application data directories. The Licensor designs the Software so that
   your audio files, media contents, analysis results and project files
   are processed locally and are not sent anywhere by the Software. Local
   logs (application diagnostics, crash records) stay on your disk until
   you choose to export or share them.

7. ACCEPTABLE USE
   You agree to use the Software only in compliance with applicable law.
   You are solely responsible for the media you analyze, render, and
   export with the Software, including respecting the rights of copyright
   and other rights holders in any audio or visual material you process.

8. DISCLAIMER OF WARRANTY
   THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
   OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
   MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, AND NONINFRINGEMENT.
   IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
   CLAIM, DAMAGES, OR OTHER LIABILITY ARISING FROM, OUT OF, OR IN
   CONNECTION WITH THE SOFTWARE OR ITS USE.

9. TERMINATION
   This license terminates automatically if you breach any term. Upon
   termination you must stop using the Software and delete all copies.

10. CHANGES
   The Licensor may revise this agreement for future releases. Continued
   use after an update constitutes acceptance of the revised terms.

<!-- proprietary-text-end -->

This document is a licensing statement prepared for the Software. It is not
legal advice.

---

## 4. Third-party software and attribution notices

The first-party proprietary license does **not** relicense third-party
dependencies. Each dependency keeps its own license, and the binary
distributions of the Software link against them under those licenses.

### 4.1 Toolchain

- **Go** (programming language and standard library), distributed by the Go
  authors under the Go license (BSD-style; see
  https://go.dev/LICENSE/). The Phase 1 module requires Go 1.27 with
  toolchain go1.27.3.

### 4.2 Linked Go module graph (actual Phase 1 dependencies)

The table below was generated from the actual module graph of the shipped
binaries (`go list -deps ./cmd/...` at Phase 1 completion) by reading each
module's license file in the module cache. License identifiers were detected
by content inspection; where a module ships a differently formatted license,
the identifier is stated with the detection method and the verbatim text
remains authoritative in the module's own file.

| Module | Version | License |
|---|---|---|
| `fyne.io/fyne/v2` | `v2.8.2` | BSD-3-Clause |
| `fyne.io/systray` | `v1.12.3-0.20260810170012-af4e8e793ec4` | Apache-2.0 |
| `github.com/BurntSushi/toml` | `v1.6.0` | MIT (module `COPYING`) |
| `github.com/anthonynsimon/bild` | `v0.14.0` | MIT |
| `github.com/clipperhouse/uax29/v2` | `v2.2.0` | MIT |
| `github.com/fsnotify/fsnotify` | `v1.9.0` | BSD-3-Clause |
| `github.com/fyne-io/image` | `v0.1.1` | BSD-3-Clause |
| `github.com/fyne-io/oksvg` | `v0.2.1-0.20260918172519-a9af55fa95d9` | BSD-3-Clause |
| `github.com/go-gl/gl` | `v0.0.0-20260331235117-4566fea9a276` | MIT |
| `github.com/go-gl/glfw/v3.4/glfw` | `v0.1.0-pre.2` | BSD-3-Clause |
| `github.com/go-text/render` | `v0.2.1` | BSD-3-Clause |
| `github.com/go-text/typesetting` | `v0.3.5` | BSD-3-Clause |
| `github.com/godbus/dbus/v5` | `v5.2.2` | BSD-2-Clause or BSD-3-Clause (module license offers both options) |
| `github.com/jeandeaual/go-locale` | `v0.0.0-20250612000132-0ef82f21eade` | MIT |
| `github.com/jsummers/gobmp` | `v0.0.0-20230614200233-a9de23ed2e25` | MIT (module `COPYING.txt`) |
| `github.com/mattn/go-runewidth` | `v0.0.24` | MIT |
| `github.com/nfnt/resize` | `v0.0.0-20180221191011-83c6a9932646` | ISC-style permissive text (verbatim in module `LICENSE`; detected by content, not SPDX header) |
| `github.com/nicksnyder/go-i18n/v2` | `v2.5.1` | MIT |
| `github.com/rymdport/portal` | `v0.4.2` | Apache-2.0 |
| `github.com/srwiley/oksvg` | `v0.0.0-20221011165216-be6e8873101c` | BSD-3-Clause |
| `github.com/srwiley/rasterx` | `v0.0.0-20220730225603-2ab79fcdd4ef` | BSD-3-Clause |
| `github.com/yuin/goldmark` | `v1.8.2` | MIT |
| `golang.org/x/image` | `v0.43.0` | BSD-3-Clause |
| `golang.org/x/net` | `v0.35.0` | BSD-3-Clause |
| `golang.org/x/sys` | `v0.30.0` | BSD-3-Clause |
| `golang.org/x/text` | `v0.38.0` | BSD-3-Clause |

Full license texts are distributed inside each module's source (Go module
proxy cache / upstream repository) and remain authoritative. No copyright
holder names have been invented in this table; where a copyright statement
matters, read the module's own license file.

### 4.3 Native platform prerequisites (not distributed by this project)

Desktop builds link against platform-provided native libraries (for example
Microsoft Win32/GDI on Windows; X11, Wayland, OpenGL/EGL drivers on Linux).
Those components are governed by their own licenses and are supplied by the
operating system, driver vendors, or the user's toolchain. The Software does
not bundle proprietary system DLLs, GPU drivers, or unlicensed media codecs.

---

## 5. Trademarks

SHEYTAN™ and the SHEYTAN logo are trademarks of Parsaetak. They identify the
Software and its distribution; no license in this repository grants any
right to use the SHEYTAN name, logo or branding beyond what trademark law
allows, and nothing here implies endorsement of derived works. The trademark
grant and restrictions are stated in §3 clause 3 (Trademark) of the
Parsaetak Proprietary License above.

---

## 6. Governance

Only the licensor (Parsaetak) may change a classification, grant an
exception, or relicense any component. Any such change must be recorded in
this document with the rationale and the date.

---

## 7. Contact

Licensing questions, proprietary-use inquiries and exceptions:
**https://github.com/Parsaetak** (use the contact channel listed there).
