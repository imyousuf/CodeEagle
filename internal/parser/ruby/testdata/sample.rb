require 'json'
require_relative 'helpers/auth'

module MyApp
  module Services
    class UserService < BaseService
      include Loggable
      extend ClassMethods

      MY_CONST = 42

      attr_reader :name
      attr_accessor :email

      def initialize(name, email)
        @name = name
        @email = email
      end

      def greet
        "Hello, #{@name}"
      end

      def process
        validate_input
        greet
      end

      private

      def validate_input
        raise "Invalid" if @name.nil?
      end
    end
  end
end
