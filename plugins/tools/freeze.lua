local output, err = omo.exec("omo", "office", "freeze")
if err ~= "" then
  error(err)
end
omo.log(output)

local instructions = [[Freeze the office now because the user may lose connectivity.

Halt your current action at a safe point and do not start or spawn new work. If you are not the CEO, run `omo wait` and remain parked until a global wake-up mail arrives.

If you are the CEO, stay at your prompt and wait for the user to tell you to unfreeze the office. Then run `omo office unfreeze`; that CEO-only command sends the global wake-up mail before resuming all spawning.]]

local delivered, send_err = omo.exec("omo", "send", "-s", "Office frozen", "-p", "urgent", instructions)
if send_err ~= "" then
  error(send_err)
end
omo.log(delivered)
