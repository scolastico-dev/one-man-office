local data = event.data or {}
local settings = config or {}
local role = tostring(data.role or "")
local branch = tostring(data.branch or "")
local text = tostring(data.text or "")
local enabled = settings.instruct ~= false
local developer_like = role == "developer" or role == "freelancer"
local marker = "<!-- pullrequest-informative-body-v1 -->"
local title_and_jobs = " Supply a scoped Conventional Commits PR title, for example `fix(company): center sidebar resizer`. In `## Jobs`, write omo job references as `job 123` or plain `123`, never `#123` because GitHub links that to a PR or issue."
local result_guidance = " Report every result line in `omo done`: `<repo>: <url> (created|updated|existing)` or `<repo>: no changes on <branch>; nothing to open`."

if enabled and role == "product_manager" then
	local note = marker .. "\nBefore `omo done`, write one authored Markdown description file in `storage`; gather facts from merged child `omo job` results and review notes because you have the whole picture. Use `## Summary` (2–4 sentences), `## What changed` (bullets by area/file group), `## Why` (the user-facing problem and decision), `## How it was verified` (exact commands/outcomes, test counts, and measured manual values), `## Risks and follow-ups`, and `## Jobs` (job IDs/titles). Use an absolute body path; relative paths are rejected. Run once with `body=<absolute-path>` using `omo plugin trigger pullrequest create -- [repo=<key>] body=<absolute-path> \"<title>\"`." .. title_and_jobs .. result_guidance
	if string.find(text, marker, 1, true) == nil then
		data.text = text .. "\n\n" .. note
	end
elseif enabled and developer_like and data.merge_target == "asis" and string.match(branch, "%S") then
	local note = marker .. "\nBefore `omo done`, write an authored Markdown description file in the worktree or a temporary path, never elsewhere inside `.omo`. Use `## Summary` (2–4 sentences), `## What changed` (bullets by area/file group), `## Why` (the user-facing problem and decision), `## How it was verified` (exact commands/outcomes, test counts, and measured manual values), `## Risks and follow-ups`, and `## Jobs` (job IDs/titles). Use an absolute body path; relative paths are rejected. Run once with `body=<absolute-path>` using `omo plugin trigger pullrequest create -- [repo=<key>] body=<absolute-path> \"<title>\"`." .. title_and_jobs .. result_guidance
	if string.find(text, marker, 1, true) == nil then
		data.text = text .. "\n\n" .. note
	end
end
