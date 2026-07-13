# Personal Chrome Attachment Plan (Superseded)

> Language: English | [简体中文](../zh-cn/docs/personal-chrome-attachment-plan.md)

Status as of 2026-07-10: SparkClaw will not attach to the owner's daily Chrome
profile and will not use Chrome as a separate visible-browser provider.

The selected design is the
[Managed Shared Chromium Profile](managed-persistent-browser-profile.md):

- hidden and visible presentations both use Chromium;
- both presentations use the same SparkClaw-owned persistent profile;
- normal work is headless;
- Chromium becomes visible only for human verification or an explicit request;
- authentication remains inside the shared Chromium profile;
- login continuation uses the actual post-login URL and does not require origin
  equality.

This file remains only to prevent older links from describing the abandoned
personal Chrome attachment design as active work.
