function process_message()
    local type, name, value, count, representation = read_next_field()
    if not(type == 0 and name == "foo" and value == "bar" and count == 1 and representation == "") then
        return 1
    end
    type, name, value, count, representation = read_next_field()
    if not(type == 1 and name == "bytes" and value == "data" and count == 1 and representation == "") then
        return 2
    end
    type, name, value, count, representation = read_next_field()
    if not(type == 2 and name == "int" and value == 999 and count == 2 and representation == "") then
        return 3
    end
    type, name, value, count, representation = read_next_field()
    if not(type == 3 and name == "double" and value == 99.9 and count == 1 and representation == "") then
        return 4
    end
    type, name, value, count, representation = read_next_field()
    if not(type == 4 and name == "bool" and value == true and count == 1 and representation == "") then
        return 5
    end
    type, name, value, count, representation = read_next_field()
    if not(type == 0 and name == "foo" and value == "alternate" and count == 1 and representation == "") then
        return 6
    end
    type, name, value, count, representation = read_next_field()
    if not(type == nil and name == nil and value == nil and count == nil and representation == nil) then
        return 7
    end

    return 0
end
