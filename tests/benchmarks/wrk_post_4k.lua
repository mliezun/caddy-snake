function generate_random_string()
    local length = 4 * 1024
    local chars = {}
    for i = 1, length do
        chars[i] = string.char(math.random(35, 90))
    end
    return table.concat(chars)
end


wrk.method = "POST"
wrk.body   = '{"content": "' .. generate_random_string() .. '"}'
wrk.headers["Content-Type"] = "application/json"
