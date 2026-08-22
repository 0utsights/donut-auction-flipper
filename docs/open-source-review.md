# Open-source review

Reviewed on 2026-08-22 before the API-only redesign.

| Project | License | Useful part | Decision |
|---|---|---|---|
| [Official Donut API schema](https://api.donutsmp.net/doc.json) | Service contract | Authoritative routes, auth, and rate limit | Primary contract |
| [cancel-cloud/Donuts-Auctions](https://github.com/cancel-cloud/Donuts-Auctions) | MIT | Real menu parsing and local analytics | Reference candidate if parsing returns; do not import its idle automation |
| [RubyImpala/Glaze](https://github.com/RubyImpala/Glaze) | GPL-3.0 | Community Donut API wrapper | Do not copy unless this project deliberately adopts GPL compatibility |
| [BKHornYT/donutah](https://github.com/BKHornYT/donutah) | No license found | Java auction scraper | Reference only; copyright does not permit copying |
| [Bees-D/donut-auto-auction](https://github.com/Bees-D/donut-auto-auction) | No license found | Automation-oriented design notes | Do not copy |
| [nicholasxdavis/autoah-donutsmp](https://github.com/nicholasxdavis/autoah-donutsmp) | MIT | Auction automation | Not relevant to notify-only goals |

The retained `internal/donutapi` implementation already has bounded response decoding, trailing-data rejection, defensive money conversion, container depth/size limits, stable best-effort listing identity, pacing, retries, and test coverage. Importing a community client would reduce rather than improve those guarantees.

Open source is not automatically safe to paste: a repository without a license does not grant reuse rights, and GPL code would impose distribution obligations on a derivative. Any future parser import should pin a source commit, preserve notices, isolate allowed functionality, and add sanitized DonutSMP fixtures plus regression tests.
