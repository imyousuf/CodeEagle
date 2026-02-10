import axios from 'axios';
import { format } from './utils';

function helper(): number {
    return 42;
}

function processData(data: string): void {
    const result = helper();
    const formatted = format(data);
    axios.get('/api/data');
}

class DataService {
    validate(data: string): boolean {
        return data.length > 0;
    }

    async process(data: string): Promise<void> {
        if (this.validate(data)) {
            const parsed = JSON.parse(data);
        }
    }
}
