import os
import json
from datetime import datetime

def helper():
    return 42

def process_data(data):
    # Same-file call
    result = helper()
    # Import-qualified call
    path = os.path.join("/tmp", "data")
    encoded = json.dumps(data)
    now = datetime.now()
    return result

class DataProcessor:
    def validate(self, data):
        return len(data) > 0

    def process(self, data):
        # self method call
        if self.validate(data):
            # Import call from method
            return json.loads(data)
        return None
