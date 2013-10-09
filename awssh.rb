#!/usr/bin/env ruby

# Copyright (c) 2013 Adrien Bustany
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.

require 'rubygems'
require 'aws-sdk'
require 'json'

# Return configuration directories in increasing order of priority
def config_dirs
	return (ENV['XDG_CONFIG_DIRS'] or ['/etc', File.expand_path('~/.config')]).reverse().map{|x| x + '/awssh'}
end

def load_config
	config = {}

	config_dirs().each do |dir|
		file_path = dir + '/config.json'

		next unless File.file? file_path

		puts "Loading configuration from #{file_path}"

		config.merge!(JSON.load(File.open(file_path)))
	end

	if config.size() == 0
		raise "No configuration files were found"
	end

	return config
end

def load_keys
	keys = {}

	config_dirs().each do |dir|
		key_dir_path = dir + '/keys'

		next unless File.directory? key_dir_path

		Dir.entries(key_dir_path).each do |e|
			next if e[0] == '.'
			next unless e.end_with? '.pem'

			id = e[0..-5] # Strip the .pem suffix

			# Filename is user@key-id
			tokens = id.split('@')

			raise "Invalid key filename: #{e}" unless tokens.size() == 2

			keys[tokens[1]] = {
				:user => tokens[0],
				:path => "#{key_dir_path}/#{e}"
			}
		end
	end

	return keys
end

def tag_set_value(tag_set, key)
	tag_set.each do |tag|
		next unless tag[:key] == key

		return tag[:value]
	end

	return nil
end

@config = load_config()
@keys = load_keys()

region = (ARGV[0] or @config['default-aws-region'] or ENV['AWS_REGION'] or 'eu-west-1')

puts "Using region #{region}"

AWS.config(
	:access_key_id => ENV['AWS_ACCESS_KEY'],
	:secret_access_key => ENV['AWS_SECRET_KEY'],
	:region => region,
)

ec2 = AWS::EC2.new

instance_map = {}
instance_i = 0

ec2.client.describe_instances().data()[:reservation_set].each do |res|
	instance = res[:instances_set][0]
	next if instance.nil?

	next unless instance[:instance_state][:name] == 'running'

	instance_map[instance_i] = {
		:ip => instance[:ip_address],
		:key => instance[:key_name],
	}

	instance_columns = [instance_i.to_s]
	
	@config['columns'].each do |c| 
		val = ''

		if c.start_with? 'tag:'
			tag_name = c[4..-1]
			val = tag_set_value(instance[:tag_set], tag_name)
		else
			val = instance[c.to_sym]
		end

		instance_columns << val
	end

	puts instance_columns.join(' ')

	instance_i += 1
end

puts "Instance number?"
number = -1

begin
	number = Integer(STDIN.readline().strip())
rescue Interrupt
	exit 0
end

instance = instance_map[number]

if instance.nil?
	raise "Invalid instance number #{number}"
end

instance_key = @keys[instance[:key]]

if instance_key.nil?
	raise "I don't have an SSH key called #{instance[:key]}"
end

puts "Connecting to #{instance[:ip]}"
exec "ssh -i #{instance_key[:path]} #{instance_key[:user]}@#{instance[:ip]}"
