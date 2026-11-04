# LICENSE — SHEYTAN Music Visualizer licensing

Copyright © 2024–2026 Parsaetak (https://github.com/Parsaetak). All rights
reserved.

This is the **sole licensing artifact** of SHEYTAN Music Visualizer ("the
Software"). It carries the complete licensing picture in one document: the
licensing model, the FULL legal text of the Parsaetak Proprietary License
v1.1, the trademark notice, the third-party attribution notices, and the
governance/contact points. No other license, licence, notice, or copying
file in this repository carries legal authority.

Product identity: SHEYTAN™ · SHEYTAN Music Visualizer · Licensor and
copyright holder: Parsaetak · Authored by Parsa Tak.

---

## 1. Licensing model

SHEYTAN Music Visualizer is distributed under a deliberately conservative
**single-license model**:

1. **Everything is Proprietary.** All first-party material in this
   repository — and every artifact built from it — is governed by the
   Parsaetak Proprietary License v1.1 (§2). This includes, non-exhaustively:
   the Go product implementation (`cmd/`, `internal/`, `ui/`), the brand
   identity constants and theme tokens, all product documentation
   (`license.md`, `readme.md`, `agent.md`, `roadmap.md`, `ai-notes.md`),
   the build and release scripts (`scripts/`), the CI machinery
   (`.github/`), all tests, and the log-capture/consolidation subsystem.
2. **No open-designated components exist in Phase 1.** Unlike the reference
   SHEYTAN-LA repository, this repository contains no Apache-2.0 component
   and no mixed-license model. Material becomes open only by an explicit,
   written designation by Parsaetak in a future revision of this document
   (see §5) — never by omission.
3. **Third-party software keeps its own licenses.** Nothing in this
   document relicenses any third-party dependency (see §4).

---

## 2. Parsaetak Proprietary License v1.1

The complete, legally binding license text governing every component of the
Software:

<!-- proprietary-text-begin -->

PARSAETAK & SHEYTAN PROPRIETARY LICENSE
=============================
Version 1.1 · Last updated: 2026-09-26

Copyright © 2024–2026 Parsaetak (https://github.com/Parsaetak). All rights
reserved. Product of Parsaetak; authored by Parsa Tak.

SCOPE OF THIS LICENSE
---------------------
This license governs every component of SHEYTAN Music Visualizer — the
SHEYTAN-specific proprietary mechanisms, the product-specific
implementation, the assets and designs, and all other first-party material
listed in §1 of LICENSE.md. No components are designated as open; there is
no open-source grant of any kind in this repository.

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
   license. This license attaches to the concrete expressions that make up
   the Software (actual code, documentation, assets, and implementations);
   it does not assert that abstract ideas, techniques, algorithms, or
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
   non-commercial personal use is permitted, provided all files — including
   this license — remain intact.

5. DERIVATIVE WORKS
   You may modify the Software for your own personal use. You may NOT
   distribute modified versions, rebranded builds, or extractions of the
   source code without prior written permission.

6. LOCAL-FIRST PRIVACY
   The Software is designed to run all audio analysis, rendering, and
   export locally on your machine. It does not send your media files,
   analysis results, or project data to any server, and it contains no
   telemetry or mandatory account. Local logs (application diagnostics,
   crash records) stay on your disk until you choose to export them.

7. ACCEPTABLE USE
   You agree to use the Software only in compliance with applicable law.
   You are solely responsible for the media files you import, process, and
   export with the Software, including securing any rights required for
   third-party music you use.

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

Scope adaptation note: this text is the Parsaetak Proprietary License v1.1
from the licensor's reference repository (SHEYTAN-LA, `LICENSE.md` §5,
checked 2026-11-04), with the defined Software changed to SHEYTAN Music
Visualizer, references to that repository's mixed-license classification
removed (none applies here), and the privacy/acceptable-use clauses adapted
to this product's local-first media scope. Substantive grant, permission,
restriction, warranty, liability, termination and trademark terms are
unchanged.

---

## 3. Trademarks

SHEYTAN™, SHEYTAN Music Visualizer and the SHEYTAN logo are trademarks of
Parsaetak. They identify the Software and its distribution; no license in
this repository grants any right to use the SHEYTAN name, logo or branding
beyond what trademark law allows, and nothing here implies endorsement of
derived works, that the Software is open source, or that third parties are
licensed to reuse SHEYTAN branding. The trademark grant and restrictions
are stated in §2 clause 3 (Trademark) above.

---

## 4. Third-party software and attribution notices

SHEYTAN Music Visualizer builds on third-party software. **Each component
remains under its own license; nothing in this repository modifies any
third-party licensing term.** Licenses below were read directly from the
license files inside the module archives of the actual module graph at
packaging time (2026-11-04). Verbatim license texts ship inside each
module's archive (available via the Go module proxy and the respective
repositories); they are reproduced here by reference, not copied.

### Go toolchain

| Component | License | Where |
|---|---|---|
| Go 1.27.3 toolchain and standard library | BSD-3-Clause (Go license) | https://go.dev/doc/devel/release — Copyright The Go Authors |

### Direct dependency

| Module | Version | License (verified in module archive) |
|---|---|---|
| fyne.io/fyne/v2 | v2.8.2 | BSD-3-Clause |

Fyne v2.8.2 (https://github.com/fyne-io/fyne, release 2026-10-07) is the
desktop UI toolkit linked into every desktop build.

### Modules linked into the Phase 1 desktop binary

| Module | Version | License (verified) |
|---|---|---|
| fyne.io/systray | v1.12.3-0.20260810170012-af4e8e793ec4 | Apache-2.0 |
| github.com/BurntSushi/toml | v1.6.0 | MIT |
| github.com/anthonynsimon/bild | v0.14.0 | MIT |
| github.com/clipperhouse/uax29/v2 | v2.2.0 | MIT |
| github.com/fsnotify/fsnotify | v1.9.0 | BSD-3-Clause |
| github.com/fyne-io/image | v0.1.1 | BSD-3-Clause |
| github.com/fyne-io/oksvg | v0.2.1-0.20260918172519-a9af55fa95d9 | BSD-3-Clause |
| github.com/go-gl/gl | v0.0.0-20260331235117-4566fea9a276 | MIT |
| github.com/go-gl/glfw/v3.4/glfw | v0.1.0-pre.2 | BSD-3-Clause |
| github.com/go-text/render | v0.2.1 | BSD-3-Clause |
| github.com/go-text/typesetting | v0.3.5 | BSD-3-Clause |
| github.com/go-text/typesetting-utils | v0.0.0-20260419141703-4ffe8874dabc | BSD-3-Clause |
| github.com/godbus/dbus/v5 | v5.2.2 | BSD-3-Clause |
| github.com/jeandeaual/go-locale | v0.0.0-20250612000132-0ef82f21eade | MIT |
| github.com/jsummers/gobmp | v0.0.0-20230614200233-a9de23ed2e25 | MIT |
| github.com/mattn/go-runewidth | v0.0.24 | MIT |
| github.com/nfnt/resize | v0.0.0-20180221191011-83c6a9932646 | ISC |
| github.com/nicksnyder/go-i18n/v2 | v2.5.1 | MIT |
| github.com/rymdport/portal | v0.4.2 | Apache-2.0 |
| github.com/srwiley/oksvg | v0.0.0-20221011165216-be6e8873101c | BSD-3-Clause |
| github.com/srwiley/rasterx | v0.0.0-20220730225603-2ab79fcdd4ef | BSD-3-Clause |
| github.com/yuin/goldmark | v1.8.2 | MIT |
| golang.org/x/image | v0.43.0 | BSD-3-Clause (Copyright The Go Authors) |
| golang.org/x/net | v0.35.0 | BSD-3-Clause (Copyright The Go Authors) |
| golang.org/x/sys | v0.30.0 | BSD-3-Clause (Copyright The Go Authors) |
| golang.org/x/text | v0.38.0 | BSD-3-Clause (Copyright The Go Authors) |

### Full module graph (superset of the linked set; build/test/tooling only)

The Go module graph additionally contains the following modules. They are
not necessarily linked into the product binaries (tooling, test, or
platform-conditional dependencies), but are listed for completeness; each
remains under its own license as verified in its module archive:

| Module | Version | License (verified) |
|---|---|---|
| github.com/FyshOS/fancyfs | v0.0.1 | BSD-3-Clause |
| github.com/akavel/rsrc | v0.10.2 | MIT |
| github.com/cpuguy83/go-md2man/v2 | v2.0.7 | MIT |
| github.com/creack/pty | v1.1.9 | MIT |
| github.com/davecgh/go-spew | v1.1.1 | ISC |
| github.com/fogleman/gg | v1.3.0 | MIT |
| github.com/fyne-io/gl-js | v0.2.1-0.20260315212741-029c47fd27e8 | BSD-3-Clause |
| github.com/fyne-io/glfw-js | v0.4.0 | MIT |
| github.com/golang/freetype | v0.0.0-20170609003504-e2365dfdc4a0 | FTL or GPLv2+ (dual license, per module LICENSE) |
| github.com/hack-pad/go-indexeddb | v0.3.2 | Apache-2.0 |
| github.com/hack-pad/safejs | v0.1.0 | Apache-2.0 |
| github.com/inconshreveable/mousetrap | v1.0.0 | Apache-2.0 |
| github.com/jackmordaunt/icns/v2 | v2.2.7 | MIT |
| github.com/josephspurrier/goversioninfo | v1.7.0 | MIT |
| github.com/kr/text | v0.2.0 | MIT |
| github.com/lucor/goinfo | v0.9.0 | BSD-3-Clause |
| github.com/mcuadros/go-version | v0.0.0-20190830083331-035f6764e8d2 | MIT |
| github.com/natefinch/atomic | v1.0.1 | MIT |
| github.com/niemeyer/pretty | v0.0.0-20200227124842-a10e7caefd8e | MIT |
| github.com/pmezard/go-difflib | v1.0.0 | BSD-3-Clause |
| github.com/russross/blackfriday/v2 | v2.1.0 | BSD-3-Clause |
| github.com/spf13/cobra | v0.0.5 | Apache-2.0 |
| github.com/spf13/pflag | v1.0.3 | BSD-3-Clause |
| github.com/stretchr/objx | v0.5.2 | MIT |
| github.com/stretchr/testify | v1.11.1 | MIT |
| github.com/urfave/cli/v2 | v2.27.7 | MIT |
| github.com/xrash/smetrics | v0.0.0-20240521201337-686a1a2994c1 | MIT |
| golang.org/x/crypto | v0.33.0 | BSD-3-Clause |
| golang.org/x/mobile | v0.0.0-20231127183840-76ac6878050a | BSD-3-Clause |
| golang.org/x/mod | v0.36.0 | BSD-3-Clause |
| golang.org/x/sync | v0.21.0 | BSD-3-Clause |
| golang.org/x/term | v0.29.0 | BSD-3-Clause |
| golang.org/x/tools | v0.45.0 | BSD-3-Clause |
| golang.org/x/tools/go/vcs | v0.1.0-deprecated | BSD-3-Clause |
| gopkg.in/check.v1 | v1.0.0-20200227125254-8fa46927fb4f | BSD-3-Clause |
| gopkg.in/yaml.v3 | v3.0.1 | Apache-2.0 |

Inventory method note: license identifiers above were classified by reading
each module's license file in the extracted Go module cache
(GOMODCACHE) during Phase 1 packaging. This inventory is informational;
the verbatim license text in each module archive is authoritative for that
module.

---

## 5. Governance — changing the licensing model

Only the repository maintainer (Parsaetak) may change this licensing model:

1. To designate a component as open in the future, this document must be
   revised by Parsaetak to name the component and its license explicitly,
   and every file in that component must carry the corresponding license
   header. There are no such components as of Phase 1.
2. Any change must be described in the repository history and reflected in
   `ai-notes.md`.

---

## 6. Contact

Licensing questions, proprietary-use inquiries and exceptions:
**https://github.com/Parsaetak** (use the contact channel listed there).

Trademark and branding inquiries: the same contact point.

---

This document is provided for licensing information. It is not legal advice.
