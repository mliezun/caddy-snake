function generate_64kb_random_string()
    local length = 64 * 1024 -- 64KB
    local chars = {}
    for i = 1, length do
        -- Choose a random printable ASCII character (32 to 126)
        chars[i] = string.char(math.random(35, 90))
    end
    return table.concat(chars)
end


wrk.method = "POST"
wrk.body   = '{"content": "' .. generate_64kb_random_string() .. '"}'
wrk.headers["Content-Type"] = "application/json"
