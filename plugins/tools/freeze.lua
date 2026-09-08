local instructions = [[Freeze the office now because the user may lose connectivity.

Halt your current action at a safe point and do not start or spawn new work. If you are not the CEO, run `omo wait` and remain parked until a global wake-up mail arrives.

If you are the CEO, first run `omo office halt-spawns`, then halt and stay at your prompt while waiting for the user. Do not use `omo wait`. When the user tells you to unfreeze the office, send a global urgent wake-up mail with `omo send`, then run `omo office resume-spawns`.]]

local delivered, send_err = omo.exec("omo", "send", "-s", "Office frozen", "-p", "urgent", instructions)
if send_err ~= "" then
  error(send_err)
end
omo.log(delivered)
