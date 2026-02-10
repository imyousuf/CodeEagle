import express from 'express';
import { getUsers, createUser, updateUser, deleteUser } from './handlers';

const app = express();
const router = express.Router();

router.get('/users', getUsers);
router.post('/users', createUser);
router.put('/users/:id', updateUser);
router.delete('/users/:id', deleteUser);
router.patch('/users/:id', (req, res) => {
  res.json({ patched: true });
});

app.use('/api/v1', router);

app.get('/health', (req, res) => {
  res.json({ status: 'ok' });
});

export default app;
