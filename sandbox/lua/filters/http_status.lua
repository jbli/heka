-- This Source Code Form is subject to the terms of the Mozilla Public
-- License, v. 2.0. If a copy of the MPL was not distributed with this
-- file, You can obtain one at http://mozilla.org/MPL/2.0/.

-- Graphs HTTP status codes collected from web server access logs.
--
-- Example Heka Configuration:
--
--  [FxaAuthServerHTTPStatus]
--  type = "SandboxFilter"
--  script_type = "lua"
--  filename = "lua_filters/http_status.lua"
--  memory_limit = 256000
--  output_limit = 64000
--  instruction_limit = 1000
--  ticker_interval = 60
--  preserve_data = true
--  message_matcher = "Logger == 'nginx.access' && Type == 'fxa-auth-server'"
--
--  [FxaAuthServerHTTPStatus.config]
--  sec_per_row = 60    # (uint - optional default: 60)
--      # Sets the size of each bucket (resolution in seconds) in the sliding
--      # window.
--  rows = 1440           # (uint - optional default: 1440)
--      # Sets the size of the sliding window i.e., 1440 rows representing 60
--      # seconds per row is a 24 sliding hour window with 1 minute resolution.

require "circular_buffer"
require "string"

local rows        = read_config("rows") or 1440
local sec_per_row = read_config("sec_per_row") or 60

status = circular_buffer.new(rows, 5, sec_per_row)
local HTTP_200          = status:set_header(1, "HTTP_200"      , "count")
local HTTP_300          = status:set_header(2, "HTTP_300"      , "count")
local HTTP_400          = status:set_header(3, "HTTP_400"      , "count")
local HTTP_500          = status:set_header(4, "HTTP_500"      , "count")
local HTTP_UNKNOWN      = status:set_header(5, "HTTP_UNKNOWN"  , "count")

function process_message ()
    local ts = read_message("Timestamp")
    local sc = read_message("Fields[status]")

    if sc >= 200 and sc < 300 then
        status:add(ts, HTTP_200, 1)
    elseif sc >= 300  and sc < 400 then
        status:add(ts, HTTP_300, 1)
    elseif sc >= 400 and sc < 500 then
        status:add(ts, HTTP_400, 1)
    elseif sc >= 500  and sc < 600 then
        status:add(ts, HTTP_500, 1)
    else
        status:add(ts, HTTP_UNKNOWN, 1)
    end

    return 0
end

function timer_event(ns)
    inject_message(status, "HTTP Status")
end
