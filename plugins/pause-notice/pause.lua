-- Tells the running agents to halt when the user pauses omo, and the ones
-- it told to continue when the pause is lifted.
--
-- omo_paused: every agent omo.agents() lists (the CEO, product managers,
-- developers, reviewers, export jobs) gets halt_message, the CEO
-- ceo_halt_message. The terminals that took it are kept in the plugin's
-- store under "halted".
--
-- omo_resumed: every agent of "halted" that still runs gets
-- resume_message; the list is cleared. An agent that started during the
-- pause was not told to halt and is not told to continue.
--
-- The texts reach an agent through omo's own notification path: one line,
-- "omo: " in front, typed into its terminal and submitted (an agent parked
-- in omo ai wait gets it as the answer of the wait). No comment is written.

local HALTED = "halted"

local DEFAULT_HALT = "the user paused omo. Stop what you are doing now: do not finish the current step, start nothing new, change no files, run no other commands and write no comments. Then wait: run omo ai wait --timeout 60m, and run it again whenever it ends without news, until omo tells you that the pause is over."
local DEFAULT_CEO_HALT = "the user paused omo. Stop what you are doing now: do not finish the current step, start nothing new, change nothing on the board, run no commands and write no comments. End your turn and do nothing until omo tells you that the pause is over."
local DEFAULT_RESUME = "the pause is over. Continue where you stopped when omo was paused."

local settings = config or {}

-- text is the configured text of key, or fallback when it is not a text
-- with something in it.
local function text(key, fallback)
  local v = settings[key]
  if type(v) == "string" and string.find(v, "%S") then
    return v
  end
  return fallback
end

local agents, why = omo.agents()
if not agents then
  omo.log("pause-notice: the running agents cannot be read: " .. tostring(why))
  return
end

if event.event == "omo_paused" then
  local halt = text("halt_message", DEFAULT_HALT)
  local ceo_halt = text("ceo_halt_message", DEFAULT_CEO_HALT)
  local halted = {}
  for _, a in ipairs(agents) do
    local message = halt
    if a.role == "ceo" then
      message = ceo_halt
    end
    local ok, failure = omo.send_to_agent(a.terminal, message)
    if ok then
      halted[#halted + 1] = a.terminal
    else
      omo.log("pause-notice: " .. tostring(a.name) .. " was not told to halt: " .. tostring(failure))
    end
  end
  omo.store_set(HALTED, halted)
  omo.log("pause-notice: told " .. tostring(#halted) .. " agents to halt")
elseif event.event == "omo_resumed" then
  local halted = omo.store_get(HALTED)
  omo.store_delete(HALTED)
  if type(halted) ~= "table" then
    return
  end
  local running = {}
  for _, a in ipairs(agents) do
    running[a.terminal] = a
  end
  local resume = text("resume_message", DEFAULT_RESUME)
  local told = 0
  for _, terminal in ipairs(halted) do
    local a = running[terminal]
    if a then
      local ok, failure = omo.send_to_agent(terminal, resume)
      if ok then
        told = told + 1
      else
        omo.log("pause-notice: " .. tostring(a.name) .. " was not told to continue: " .. tostring(failure))
      end
    end
  end
  omo.log("pause-notice: told " .. tostring(told) .. " agents to continue")
end
