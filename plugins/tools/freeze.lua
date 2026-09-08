local output, err = omo.exec("omo", "office", "freeze")
if err ~= "" then
  error(err)
end
omo.log(output)
