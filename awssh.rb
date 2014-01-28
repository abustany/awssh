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
require 'getoptlong'
require 'json'

HelpMessage = <<EOF
Usage: #{$0} [OPTIONS]
Simple SSH launcher to connect to Amazon EC2 instances.

Options:
  -h, --help           Display this message
  -r, --region=REGION  Use the given REGION for listing instances
  -m, --match=MATCH    Only list results that match MATCH
                       A name matches if it contains all the letters from MATCH
                       in order (eg. "tmh" matches "thismatches").
  -e, --equal=VALUE    Only list results that have a column equal to VALUE
EOF

# Return configuration directories in increasing order of priority
def config_dirs
	dirs = (ENV['XDG_CONFIG_DIRS'] or '').split(':').reject {|x| x == ''}
	dirs << '/etc'
	dirs << File.expand_path('~/.config')

	return dirs.reverse().map{|x| x + '/awssh'}
end

def load_config
	config = {}

	config_dirs().each do |dir|
		file_path = dir + '/config.json'

		next unless File.file? file_path

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

def fuzzymatch(needle, haystack)
	return true if (needle.nil? or needle == '')
	return false if haystack.nil?

	idx = haystack.index(needle[0])

	return false if idx.nil?
	return fuzzymatch(needle[1..-1], haystack[idx..-1])
end

def print_instance_table(table)
	table = table.clone

	col_width = table[0].map { |x| 0 }

	table.each do |line|
		line.each_with_index do |col, i|
			col_width[i] = [col_width[i], col.to_s().size()].max
		end
	end

	table.insert(1, col_width.map { |x| "-" * x })

	table.each do |line|
		out = []

		line.each_with_index do |col, i|
			out << (" " + col.to_s().ljust(col_width[i]) + " ")
		end

		puts out.join("|")
	end
end

@config = load_config()
@keys = load_keys()

region = (@config['default-aws-region'] or ENV['AWS_REGION'] or 'eu-west-1')
fuzzy_match_value = nil
exact_match_value = nil

opts = GetoptLong.new(
	['--help', '-h', GetoptLong::NO_ARGUMENT],
	['--region', '-r', GetoptLong::REQUIRED_ARGUMENT],
	['--match', '-m', GetoptLong::REQUIRED_ARGUMENT],
	['--equal', '-e', GetoptLong::REQUIRED_ARGUMENT]
)

opts.each do |opt, arg|
	case opt
	when '--help'
		puts HelpMessage
		exit 0
	when '--region'
		region = arg.to_s
	when '--match'
		fuzzy_match_value = arg.to_s
	when '--equal'
		exact_match_value = arg.to_s
	end
end

puts "Using region #{region}"

AWS.config(
	:access_key_id => ENV['AWS_ACCESS_KEY'],
	:secret_access_key => ENV['AWS_SECRET_KEY'],
	:region => region,
	:proxy_uri => ENV['http_proxy']
)

ec2 = AWS::EC2.new

instance_map = {}
instance_i = 0

instance_table = []

# Add table header (instance index is first column)
instance_table << [''] + @config['columns']

# Build table contents
ec2.client.describe_instances().data()[:reservation_set].each do |res|
	instance = res[:instances_set][0]
	next if instance.nil?

	next unless instance[:instance_state][:name] == 'running'

	instance_columns = [instance_i.to_s]

	instance_matches = false
	
	@config['columns'].each do |c| 
		val = ''

		if c.start_with? 'tag:'
			tag_name = c[4..-1]
			val = tag_set_value(instance[:tag_set], tag_name)
		else
			val = instance[c.to_sym]
		end

		if fuzzy_match_value
			instance_matches |= fuzzymatch(fuzzy_match_value, val)
		end

		if exact_match_value
			instance_matches |= (exact_match_value == val)
		end

		if not (fuzzy_match_value || exact_match_value)
			instance_matches = true
		end

		instance_columns << val
	end

	next unless instance_matches

	instance_map[instance_i] = {
		:id => instance[:instance_id],
		:ip => instance[:ip_address],
		:key => instance[:key_name],
	}

	instance_table << instance_columns

	instance_i += 1
end

instance = nil

if instance_map.size() == 0
	puts "No AWS instance available in this region"
	exit 0
elsif instance_map.size() > 1
	# Format and print table
	print_instance_table(instance_table)

	puts "Instance number?"
	number = -1

	begin
		number = Integer(STDIN.readline().strip())
	rescue Interrupt
		exit 0
	end

	instance = instance_map[number]
else
	instance = instance_map[0]
end

if instance.nil?
	raise "Invalid instance number #{number}"
end

instance_key = @keys[instance[:key]]

if instance_key.nil?
	raise "I don't have an SSH key called #{instance[:key]}"
end

cmd = ARGV.join(' ')
ssh_params = "-i #{instance_key[:path]} -t"

if @config['disable-host-key-check'] == true
	ssh_params += " -o 'StrictHostKeyChecking no' -o 'UserKnownHostsFile /dev/null'"
end

if cmd.size() > 0
	puts "Running command on #{instance[:id]} (#{instance[:ip]}): #{cmd}"
else
	puts "Connecting to #{instance[:id]} (#{instance[:ip]})"
end

# -i to point at the right SSH key
# -t to request a TTY (else sudo would not work where requiretty is set in the
# PAM settings)
# StrictHostKeyChecking and UserKnownHostsFile disabled so that we don't need to
# confirm connection to new instances
exec "ssh #{ssh_params} #{instance_key[:user]}@#{instance[:ip]} #{ARGV.join(' ')}"
